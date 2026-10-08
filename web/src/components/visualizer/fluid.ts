import type { BeatFrame } from '@/lib/beat'

// The palette fluid (MAD-779): the album's colors flowing like ink in
// water, drawn by a fragment shader. Domain-warped noise (one layer of
// noise bending the next) makes the flow; the bass pushes the warp, the
// bar swells it, the beat lights its ridges, and the song's energy sets
// how fast it moves.
//
// Rendered below the screen's resolution and scaled up (it's all soft
// gradients, so nobody can tell). The scale adapts: a slow TV stick
// draws fewer pixels until it keeps up, a fast GPU more.

const VERTEX = `
attribute vec2 p;
void main() { gl_Position = vec4(p, 0.0, 1.0); }
`

const FRAGMENT = `
// Full precision where there is any: phones run mediump at half
// precision, which turns the noise's hash to mush.
#ifdef GL_FRAGMENT_PRECISION_HIGH
precision highp float;
#else
precision mediump float;
#endif
uniform vec2 uRes;
uniform float uTime;
uniform vec3 uDark, uDominant, uVibrant, uLight;
uniform float uBass, uHigh, uSwell, uBeat, uEnergy, uPresence;

vec2 hash(vec2 p) {
  p = vec2(dot(p, vec2(127.1, 311.7)), dot(p, vec2(269.5, 183.3)));
  return -1.0 + 2.0 * fract(sin(p) * 43758.5453123);
}

float noise(vec2 p) {
  vec2 i = floor(p);
  vec2 f = fract(p);
  vec2 u = f * f * (3.0 - 2.0 * f);
  return mix(mix(dot(hash(i), f), dot(hash(i + vec2(1.0, 0.0)), f - vec2(1.0, 0.0)), u.x),
             mix(dot(hash(i + vec2(0.0, 1.0)), f - vec2(0.0, 1.0)), dot(hash(i + vec2(1.0, 1.0)), f - vec2(1.0, 1.0)), u.x), u.y);
}

float fbm(vec2 p) {
  float v = 0.0;
  float a = 0.5;
  mat2 m = mat2(1.6, 1.2, -1.2, 1.6);
  for (int i = 0; i < 5; i++) {
    v += a * noise(p);
    p = m * p;
    a *= 0.5;
  }
  return v;
}

void main() {
  vec2 uv = (gl_FragCoord.xy - 0.5 * uRes) / uRes.y;
  float t = uTime;
  vec2 q = vec2(fbm(uv * 0.9 + t * 0.06), fbm(uv * 0.9 + vec2(5.2, 1.3) - t * 0.05));
  float warp = 2.6 + uBass * 1.4 + uSwell * 0.7;
  vec2 r = vec2(fbm(uv * 0.9 + warp * q + vec2(1.7, 9.2) + t * 0.09),
                fbm(uv * 0.9 + warp * q + vec2(8.3, 2.8) - t * 0.07));
  float f = fbm(uv * 0.9 + warp * r);
  // f is roughly -0.6..0.6: spread it over 0..1.
  float ff = clamp(f * 1.3 + 0.5, 0.0, 1.0);

  vec3 col = mix(uDark, uDominant, smoothstep(0.05, 0.6, ff));
  col = mix(col, uVibrant, smoothstep(0.3, 0.85, length(q) * 1.6 + 0.25 * ff) * (0.6 + 0.35 * uEnergy));
  // The brightest folds catch the light: more on the beat and with the treble.
  col = mix(col, uLight, pow(smoothstep(0.55, 1.0, ff), 2.0) * (0.35 + 0.45 * uBeat + 0.2 * uHigh));
  col *= 0.8 + 0.4 * ff + 0.25 * uSwell;
  col *= 1.0 - 0.25 * dot(uv, uv);
  gl_FragColor = vec4(mix(uDark * 0.6, col, uPresence), 1.0);
}
`

export type Rgb = [number, number, number]
export type FluidColors = { dark: Rgb; dominant: Rgb; vibrant: Rgb; light: Rgb }

export class Fluid {
  private canvas: HTMLCanvasElement
  private gl: WebGLRenderingContext
  private uniforms: Record<string, WebGLUniformLocation | null> = {}
  private time = Math.random() * 100
  private scale = 0.5
  private slow = 0
  private fast = 0

  /** Throws if the device has no WebGL. */
  constructor(canvas: HTMLCanvasElement) {
    this.canvas = canvas
    const gl = canvas.getContext('webgl', { antialias: false, alpha: false, powerPreference: 'high-performance' })
    if (!gl) throw new Error('no WebGL')
    this.gl = gl
    const program = gl.createProgram()
    gl.attachShader(program, this.shader(gl.VERTEX_SHADER, VERTEX))
    gl.attachShader(program, this.shader(gl.FRAGMENT_SHADER, FRAGMENT))
    gl.linkProgram(program)
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(program) ?? 'link failed')
    gl.useProgram(program)
    const buf = gl.createBuffer()
    gl.bindBuffer(gl.ARRAY_BUFFER, buf)
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW)
    const loc = gl.getAttribLocation(program, 'p')
    gl.enableVertexAttribArray(loc)
    gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0)
    for (const u of ['uRes', 'uTime', 'uDark', 'uDominant', 'uVibrant', 'uLight', 'uBass', 'uHigh', 'uSwell', 'uBeat', 'uEnergy', 'uPresence']) {
      this.uniforms[u] = gl.getUniformLocation(program, u)
    }
  }

  private shader(type: number, src: string) {
    const gl = this.gl
    const s = gl.createShader(type)!
    gl.shaderSource(s, src)
    gl.compileShader(s)
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s) ?? 'compile failed')
    return s
  }

  render(f: BeatFrame, c: FluidColors) {
    const { gl, canvas, uniforms: u } = this
    this.adapt(f.dt)
    const w = Math.max(1, Math.round(canvas.clientWidth * this.scale))
    const h = Math.max(1, Math.round(canvas.clientHeight * this.scale))
    if (canvas.width !== w || canvas.height !== h) {
      canvas.width = w
      canvas.height = h
    }
    gl.viewport(0, 0, w, h)
    // Loud songs flow faster; it never quite stops.
    this.time += (f.dt / 1000) * (0.25 + f.energy * 0.9 + f.swell * 0.3)
    const [b0, , b2, b3] = f.bands
    gl.uniform2f(u.uRes, w, h)
    gl.uniform1f(u.uTime, this.time)
    gl.uniform3fv(u.uDark, c.dark)
    gl.uniform3fv(u.uDominant, c.dominant)
    gl.uniform3fv(u.uVibrant, c.vibrant)
    gl.uniform3fv(u.uLight, c.light)
    gl.uniform1f(u.uBass, Math.min(1, b0))
    gl.uniform1f(u.uHigh, Math.min(1, (b2 + b3) / 2))
    gl.uniform1f(u.uSwell, Math.min(1.5, f.swell))
    gl.uniform1f(u.uBeat, Math.min(1.5, f.beat))
    gl.uniform1f(u.uEnergy, f.energy)
    gl.uniform1f(u.uPresence, Math.max(0.35, f.presence))
    gl.drawArrays(gl.TRIANGLES, 0, 3)
  }

  /** Fewer pixels while frames run long, more while they're quick. */
  private adapt(dt: number) {
    if (dt > 24) this.slow++
    else this.slow = Math.max(0, this.slow - 1)
    if (dt < 15) this.fast++
    else this.fast = 0
    if (this.slow > 20 && this.scale > 0.25) {
      this.scale -= 0.05
      this.slow = 0
    } else if (this.fast > 120 && this.scale < 0.75) {
      this.scale += 0.05
      this.fast = 0
    }
  }

  dispose() {
    this.gl.getExtension('WEBGL_lose_context')?.loseContext()
  }
}

let probe: CanvasRenderingContext2D | null = null

/** A CSS color (any syntax the browser knows, oklch included) as 0–1 RGB. */
export function toRgb(css: string): Rgb {
  probe ??= document.createElement('canvas').getContext('2d', { willReadFrequently: true })
  if (!probe) return [0.5, 0.5, 0.5]
  probe.clearRect(0, 0, 1, 1)
  probe.fillStyle = '#000'
  probe.fillStyle = css
  probe.fillRect(0, 0, 1, 1)
  const [r, g, b] = probe.getImageData(0, 0, 1, 1).data
  return [r / 255, g / 255, b / 255]
}
