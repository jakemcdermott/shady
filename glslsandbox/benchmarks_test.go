package glslsandbox

import (
	"context"
	"image"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/polyfloyd/shady"
)

const shaderPlain = `
	void main(void) {
		gl_FragColor = vec4(1.0, 0.0, 0.0, 1.0);
	}
`
const shaderWave = `
	void main(void) {
		gl_FragColor = vec4(cos(gl_FragCoord.x), sin(gl_FragCoord.y), 0.0, 1.0);
	}
`

var sources = map[string]string{
	"plain": shaderPlain,
	"wave":  shaderWave,
}

func BenchmarkCompile(b *testing.B) {
	if os.Getenv("DISPLAY") == "" {
		b.SkipNow()
	}

	for name, source := range sources {
		env := GLSLSandbox{Source: source}
		b.Run(name, func(b *testing.B) {
			runtime.LockOSThread()

			for n := 0; n < b.N; n++ {
				shader, err := glsl.NewShader(512, 512, env)
				if err != nil {
					b.Fatal(err)
				}
				shader.Close()
			}
		})
	}
}

func BenchmarkRenderImage(b *testing.B) {
	if os.Getenv("DISPLAY") == "" {
		b.SkipNow()
	}

	for name, source := range sources {
		env := GLSLSandbox{Source: source}
		b.Run(name, func(b *testing.B) {
			runtime.LockOSThread()

			shader, err := glsl.NewShader(512, 512, env)
			if err != nil {
				b.Fatal(err)
			}
			defer shader.Close()

			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				shader.Image()
			}
		})
	}
}

func BenchmarkRenderAnimation(b *testing.B) {
	if os.Getenv("DISPLAY") == "" {
		b.SkipNow()
	}

	for name, source := range sources {
		env := GLSLSandbox{Source: source}
		b.Run(name, func(b *testing.B) {
			runtime.LockOSThread()

			shader, err := glsl.NewShader(512, 512, env)
			if err != nil {
				b.Fatal(err)
			}
			defer shader.Close()

			ctx, cancel := context.WithCancel(context.Background())
			stream := make(chan image.Image)
			go func() {
				for n := 0; n < b.N; n++ {
					<-stream
				}
				cancel()
			}()
			b.ResetTimer()
			shader.Animate(ctx, time.Millisecond, stream)
			close(stream)
		})
	}
}
