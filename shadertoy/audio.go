package shadertoy

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"github.com/go-gl/gl/v3.3-core/gl"
	"github.com/mjibson/go-dsp/fft"
	"github.com/polyfloyd/shady"
)

const audioTexWidth = 512

var (
	audioGenericValueRe = regexp.MustCompile("^([^;]+)$")
	audioPCMValueRe     = regexp.MustCompile("^([^;]+);(\\d+):(\\d+):([su]\\d{1,2}[lb]e)$")
)

type format string

func (f format) Bits() int {
	s := f[1:3]
	if s[1] < '0' || '9' < s[1] {
		s = s[:1]
	}
	b, err := strconv.Atoi(string(s))
	if err != nil {
		panic(err)
	}
	return b
}

func parseMappingValue(pwd, value string) (audioSource, error) {
	if match := audioGenericValueRe.FindStringSubmatch(value); match != nil {
		channels, samplerate, ft, pcmStream, err := decodeAudioFile(match[1])
		if err != nil {
			return nil, err
		}
		return &rawSource{
			file:       pcmStream,
			sampleRate: samplerate,
			channels:   channels,
			format:     ft,
		}, nil
	}

	match := audioPCMValueRe.FindStringSubmatch(value)
	if match == nil {
		return nil, fmt.Errorf("could not parse audio value: %q (format: %s)", value, audioPCMValueRe)
	}
	filename := resolvePath(pwd, match[1])
	samplerate, err := strconv.Atoi(match[2])
	if err != nil {
		return nil, err
	}
	channels, err := strconv.Atoi(match[3])
	if err != nil {
		return nil, err
	}
	format := format(match[4])
	if format.Bits()%8 != 0 {
		return nil, fmt.Errorf("the number of PCM sample bits must be a multiple of 8, format: %q", format)
	}

	fd, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("could not open audio source: %v", err)
	}
	return &rawSource{
		file:       fd,
		sampleRate: samplerate,
		channels:   channels,
		format:     format,
	}, nil
}

// audioTexture is a mapping of an audio stream.
type audioTexture struct {
	uniformName string
	id          uint32
	index       uint32
	source      audioSource
}

func newAudioTexture(uniformName string, source audioSource) (*audioTexture, error) {
	at := &audioTexture{
		uniformName: uniformName,
		index:       texIndexEnum,
		source:      source,
	}
	texIndexEnum++
	gl.GenTextures(1, &at.id)
	gl.BindTexture(gl.TEXTURE_2D, at.id)

	var initialData [audioTexWidth * 2]byte
	gl.TexImage2D(
		gl.TEXTURE_2D,          // target
		0,                      // level
		glLUMINANCE,            // internalFormat
		audioTexWidth,          // width
		2,                      // height
		0,                      // border
		glLUMINANCE,            // format
		gl.UNSIGNED_BYTE,       // type
		gl.Ptr(initialData[:]), // data
	)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	return at, nil
}

func (at *audioTexture) PreRender(uniforms map[string]glsl.Uniform, state glsl.RenderState) {
	period := at.source.ReadSamples(state.Interval)
	if len(period) < audioTexWidth {
		period = make([]float64, audioTexWidth)
	} else {
		period = period[len(period)-audioTexWidth:]
	}

	if loc, ok := uniforms[at.uniformName]; ok {
		freqs := fft.FFTReal(period)
		textureData := make([]uint8, audioTexWidth*2)
		for x := 0; x < audioTexWidth/2; x++ {
			// FFT
			textureData[x*2] = uint8((real(freqs[x])*0.5 + 0.5) * 255.0)
			textureData[x*2+1] = uint8((imag(freqs[x])*0.5 + 0.5) * 255.0)
		}
		for x := 0; x < audioTexWidth; x++ {
			// Wave
			textureData[audioTexWidth+x] = uint8((period[x]*0.5 + 0.5) * 255.0)
		}
		gl.BindTexture(gl.TEXTURE_2D, at.id)
		gl.TexSubImage2D(
			gl.TEXTURE_2D,       // target,
			0,                   // level,
			0,                   // xoffset,
			0,                   // yoffset,
			audioTexWidth,       // width,
			2,                   // height,
			glLUMINANCE,         // format,
			gl.UNSIGNED_BYTE,    // type,
			gl.Ptr(textureData), // data
		)
		gl.ActiveTexture(gl.TEXTURE0 + at.index)
		gl.Uniform1i(loc.Location, int32(at.index))
	}
	if m := ichannelNumRe.FindStringSubmatch(at.uniformName); m != nil {
		if loc, ok := uniforms[fmt.Sprintf("iChannelResolution[%s]", m[1])]; ok {
			gl.Uniform3f(loc.Location, float32(audioTexWidth), float32(2), 1.0)
		}
	}
	if m := ichannelNumRe.FindStringSubmatch(at.uniformName); m != nil {
		if loc, ok := uniforms[fmt.Sprintf("iChannelTime[%s]", m[1])]; ok {
			gl.Uniform1f(loc.Location, float32(state.Time)/float32(time.Second))
		}
	}
	if loc, ok := uniforms["iSampleRate"]; ok {
		gl.Uniform1f(loc.Location, at.source.SampleRate())
	}
}

type audioSource interface {
	SampleRate() float32

	ReadSamples(period time.Duration) []float64
}

type rawSource struct {
	file                 io.Reader
	sampleRate, channels int
	format               format
}

func (s rawSource) SampleRate() float32 {
	return float32(s.sampleRate)
}

func (s *rawSource) ReadSamples(period time.Duration) []float64 {
	numBytes := s.format.Bits() / 8
	buf := make([]byte, (time.Duration(s.sampleRate*s.channels*numBytes)*period)/time.Second)
	n, err := io.ReadAtLeast(s.file, buf, len(buf))
	if err != nil {
		return make([]float64, time.Duration(s.sampleRate)*period/time.Second)
	}

	samples := make([]float64, n/numBytes)
	switch s.format {
	case "s16le":
		for i := range samples {
			offset := i * s.channels * numBytes
			bytes := buf[offset : offset+numBytes]
			sample := int16(bytes[0]) | int16(bytes[1])<<8
			samples[i] = float64(sample) / float64(0x7fff)
		}
	default:
		panic(fmt.Sprintf("Unimplemented format %q", s.format))
	}
	return samples
}

func decodeAudioFile(filename string) (channels, samplerate int, ft format, stream io.Reader, err error) {
	r, w := io.Pipe()
	go func() {
		cmd := exec.Command(
			"ffmpeg",
			"-i", filename,
			"-f", "s16le",
			"-acodec", "pcm_s16le",
			"-ac", "1",
			"-ar", "22000",
			"-",
		)
		cmd.Stdout = w
		if err := cmd.Run(); err != nil {
			w.CloseWithError(err)
			return
		}
		w.Close()
	}()
	return 1, 22000, "s16le", r, nil
}
