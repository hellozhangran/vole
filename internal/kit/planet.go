package kit

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// planetShader 在 GPU 上程序化画一颗带云带的星球：噪声扰动的纬向条纹 + 漫反射光照 + 边缘抗锯齿。
// 全部由公式生成，不使用任何图片素材。
var planetShader = shader.MustCompile(`package main

var Time float
var Light vec4
var Mid vec4
var Dark vec4

func hash(p vec2) float {
	return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453)
}

func noise(p vec2) float {
	i := floor(p)
	f := fract(p)
	u := f * f * (3 - 2*f)
	a := hash(i)
	b := hash(i + vec2(1, 0))
	c := hash(i + vec2(0, 1))
	d := hash(i + vec2(1, 1))
	return mix(mix(a, b, u.x), mix(c, d, u.x), u.y)
}

func Fragment(dst vec4, pos vec2) vec4 {
	size := rectSize()
	r := min(size.x, size.y) * 0.5
	p := (pos - size*0.5) / r
	d := length(p)
	if d > 1 {
		return vec4(0, 0, 0, 0)
	}
	z := sqrt(max(1-d*d, 0))
	lon := atan2(p.x, z) + Time*0.12
	lat := p.y
	warp := noise(vec2(lon*2.0, lat*5.0)) * 2.4
	bands := sin(lat*10.0+warp)*0.5 + 0.5
	swirl := noise(vec2(lon*4.0, lat*16.0))
	t := clamp(bands*0.75+swirl*0.25, 0, 1)
	col := mix(Dark.rgb, Mid.rgb, smoothstep(0.0, 0.55, t))
	col = mix(col, Light.rgb, smoothstep(0.55, 1.0, t))
	n := vec3(p.x, p.y, z)
	light := normalize(vec3(-0.55, -0.6, 0.75))
	diff := clamp(dot(n, light), 0, 1)
	col = col * (0.18 + 0.9*diff)
	col = col + pow(1-z, 4)*0.06
	alpha := 1 - smoothstep(1-1.5/r, 1, d)
	return vec4(col*alpha, alpha)
}
`)

// planetEpoch 让自转相位跨帧连续。
var planetEpoch = time.Now()

// Planet 画一颗 size×size 的程序化星球；spinning 时缓慢自转（30fps，省电），否则静止。
func Planet(c *ui.Context, size float32, colors [3]ui.Color, spinning bool) ui.Element {
	return ui.Box(c).Size(size, size).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		t := float32(0)
		if spinning {
			t = float32(p.Now().Sub(planetEpoch).Seconds())
			p.AnimationFrameRate(30)
		}
		// 柔和投影，让球体浮在背景上
		p.Shadow(r, r.W/2, 0, r.W*0.06, r.W*0.18, 0, ui.RGBA(0, 0, 0, 0.35))
		planetShader.Draw(p, r, shader.Options{Uniforms: map[string]any{
			"Time": t, "Light": colors[0], "Mid": colors[1], "Dark": colors[2],
		}})
	})
}
