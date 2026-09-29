/* m-ui Liquid Glass switch (.lg-toggle)
 *
 * Port of LiquidToggle.kt + DampedDragAnimation.kt as re-implemented for the
 * web by liquid-glass-webgl (https://github.com/martin65536/liquid-glass-webgl,
 * Apache-2.0, itself a port of Kyant0/AndroidLiquidGlass). The spring solvers,
 * the lens refraction (circleMap + SDF gradient + 7-tap dispersion), the
 * toggle CombinedBackdrop and the Ambient rim highlight follow that project's
 * renderer/spring.ts, renderer/methods-toggle.ts,
 * renderer/methods-render-glass-element-pass-toggle.ts, shaders/element*.ts
 * and shaders/highlight.ts. See NOTICE for the license terms.
 *
 * Behaviour (faithful to the original):
 *   - At rest the knob is an opaque white pebble on a gray / green track.
 *   - Pressing (pointer down, drag, or a tap/keyboard toggle) springs the knob
 *     to 1.5x and fades the white out, turning it into a clear glass lens that
 *     refracts the card colour plus a scaled copy of the track colour.
 *   - The press releases once the value has settled near its target.
 *   - Velocity squash/stretch only comes from real drag velocity; taps and
 *     keyboard toggles have zero velocity, so there is no jelly wobble.
 *
 * Geometry: iOS 26 is a 64x28 track with a 40x24 knob. m-ui uses macOS-sized
 * switches on wide screens and iOS-sized ones on narrow screens (see
 * liquid-toggle.css); every length below is scaled by trackH / 28.
 *
 * The native checkbox stays on top (invisible) and keeps form serialization,
 * focus, Space activation, reset and disabled behaviour.
 */
(() => {
  'use strict';
  if (window.MUILiquidToggleLoaded) return;
  window.MUILiquidToggleLoaded = true;

  const SELECTOR = '.switch > input[type="checkbox"], .theme-switch > input[type="checkbox"], .ultra-check > input[type="checkbox"], .check-box > input[type="checkbox"], .drawer .check > input[type="checkbox"], #client-modal .check > input[type="checkbox"]';

  const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');

  const isDark = () => document.body.classList.contains('dark') ||
    document.documentElement.classList.contains('dark') ||
    document.documentElement.getAttribute('data-theme') === 'dark' ||
    document.documentElement.getAttribute('data-theme') === 'ultra-dark';

  const isRtl = el => document.documentElement.dir === 'rtl' || getComputedStyle(el).direction === 'rtl';

  /* ---------------------------------------------------------------- *
   * Springs — closed-form solutions (renderer/spring.ts).
   * ---------------------------------------------------------------- */
  const THRESHOLD = 0.003;
  const VALUE_OMEGA = Math.sqrt(1000);            // spring(1f, 1000f)  value + press
  const SCALE_X = { omega: Math.sqrt(250), zeta: 0.6 }; // spring(0.6f, 250f)
  const SCALE_Y = { omega: Math.sqrt(250), zeta: 0.7 }; // spring(0.7f, 250f)
  const VELOCITY = { omega: Math.sqrt(300), zeta: 0.5 }; // spring(0.5f, 300f)
  const PRESSED_SCALE = 1.5;

  function springCritical(x, v, target, dt, omega) {
    const x0 = x - target;
    const decay = Math.exp(-omega * dt);
    const offset = x0 * decay + (v + omega * x0) * dt * decay;
    const nv = -omega * x0 * decay + (v + omega * x0) * (decay - omega * dt * decay);
    return [target + offset, nv];
  }

  function springUnder(x, v, target, dt, omega, zeta) {
    const x0 = x - target;
    const omegaD = omega * Math.sqrt(1 - zeta * zeta);
    const decay = Math.exp(-zeta * omega * dt);
    const cos = Math.cos(omegaD * dt);
    const sin = Math.sin(omegaD * dt);
    const b0 = (v + zeta * omega * x0) / omegaD;
    const offset = x0 * decay * cos + b0 * decay * sin;
    const nv = -zeta * omega * offset + decay * (-x0 * omegaD * sin + b0 * omegaD * cos);
    return [target + offset, nv];
  }

  // Least-squares velocity over the last 100 ms (renderer/velocity-tracker.ts).
  function trackerVelocity(samples) {
    if (samples.length < 2) return 0;
    const now = samples[samples.length - 1].t;
    let n = 0, sT = 0, sP = 0, sTT = 0, sTP = 0;
    for (let i = samples.length - 1; i >= 0; i--) {
      const s = samples[i];
      if (s.t < now - 100) break;
      const t = (s.t - now) / 1000;
      n++; sT += t; sP += s.p; sTT += t * t; sTP += t * s.p;
    }
    if (n < 2) return 0;
    const den = n * sTT - sT * sT;
    return Math.abs(den) < 1e-9 ? 0 : (n * sTP - sT * sP) / den;
  }

  /* ---------------------------------------------------------------- *
   * Shader — one full-canvas pass per switch: track, outer shadow, knob.
   * All lengths are buffer pixels. The knob is evaluated in its ORIGINAL
   * (unscaled) space and the refraction offset is mapped back through the
   * layer scale, exactly like shaders/element.ts.
   * ---------------------------------------------------------------- */
  const OFFSCREEN_W = 512;
  const OFFSCREEN_H = 256;

  const VERT_SRC = 'attribute vec2 aPos; void main(){ gl_Position = vec4(aPos, 0.0, 1.0); }';

  const FRAG_SRC = `
    precision highp float;
    uniform float uTop;            // offscreen height (gl_FragCoord y flip)
    uniform vec2  uTrackC;
    uniform vec2  uTrackHalf;
    uniform vec4  uTrackColor;     // lerp(off, on, fraction), straight alpha
    uniform vec3  uBackdrop;       // solid card colour behind the switch
    uniform vec2  uKnobC;
    uniform vec2  uKnobHalf;       // original (unscaled) half size
    uniform vec2  uLayerScale;     // press scale x velocity squash
    uniform float uPress;
    uniform float uRefrH;          // refraction height  (x press)
    uniform float uRefrA;          // refraction amount  (x press, negative)
    uniform float uBlur;           // 8dp x (1 - press)
    uniform vec2  uSTrackC;        // scaled track copy seen through the lens
    uniform vec2  uSTrackHalf;
    uniform float uSTrackR;
    uniform float uShadowR;
    uniform float uShadowA;
    uniform vec2  uShadowOff;
    uniform float uInnerR;
    uniform float uInnerA;
    uniform vec2  uInnerOff;
    uniform float uHlWidth;
    uniform float uHlAlpha;

    float sdRoundedRect(vec2 p, vec2 b, float r) {
      vec2 q = abs(p) - b + vec2(r);
      return length(max(q, 0.0)) + min(max(q.x, q.y), 0.0) - r;
    }
    vec2 gradSdRoundedRect(vec2 p, vec2 b, float r) {
      vec2 q = abs(p) - (b - vec2(r));
      if (q.x >= 0.0 || q.y >= 0.0) {
        vec2 v = max(q, 0.0); float l = length(v);
        if (l < 1e-6) return vec2(0.0);
        return sign(p) * (v / l);
      }
      float gx = step(q.y, q.x);
      return sign(p) * vec2(gx, 1.0 - gx);
    }
    float circleMap(float x) { return 1.0 - sqrt(max(0.0, 1.0 - x * x)); }
    vec4 over(vec4 dst, vec4 src) { return src + dst * (1.0 - src.a); }

    // CombinedBackdrop: solid card colour + track colour scaled around the knob.
    vec3 backdrop(vec2 px) {
      vec3 c = uBackdrop;
      if (uTrackColor.a > 0.001 && uSTrackHalf.x > 0.5 && uSTrackHalf.y > 0.5) {
        float sd = sdRoundedRect(px - uSTrackC, uSTrackHalf, uSTrackR);
        float aa = max(uBlur, 1.0);
        float m = 1.0 - smoothstep(-aa, aa, sd);
        c = mix(c, uTrackColor.rgb, uTrackColor.a * m);
      }
      return c;
    }

    void main() {
      vec2 px = vec2(gl_FragCoord.x, uTop - gl_FragCoord.y);
      vec4 col = vec4(0.0);

      // Track.
      float tr = uTrackHalf.y;
      float sdT = sdRoundedRect(px - uTrackC, uTrackHalf, tr);
      float aT = clamp(0.5 - sdT, 0.0, 1.0) * uTrackColor.a;
      col = vec4(uTrackColor.rgb * aT, aT);

      vec2 kh = uKnobHalf;
      float r = min(kh.x, kh.y);
      float minScale = min(uLayerScale.x, uLayerScale.y);
      vec2 local = (px - uKnobC) / uLayerScale;
      float sdK = sdRoundedRect(local, kh, r);

      // Outer shadow — Shadow(radius = 4dp, Black 5%, offset radius / 6).
      float sdS = sdRoundedRect((px - uKnobC - uShadowOff) / uLayerScale, kh, r) * minScale;
      float sh = uShadowA * (1.0 - smoothstep(-uShadowR, uShadowR, sdS));
      col = over(col, vec4(0.0, 0.0, 0.0, sh));

      float cov = clamp(0.5 - sdK * minScale, 0.0, 1.0);
      if (cov > 0.0) {
        vec3 kc = backdrop(px);
        float gradR = min(r * 1.5, min(kh.x, kh.y));
        vec2 grad = gradSdRoundedRect(local, kh, gradR);

        // Lens refraction with 7-path chromatic dispersion.
        if (uRefrH > 0.5 && (-sdK) < uRefrH) {
          float d = circleMap(1.0 - (-min(sdK, 0.0)) / uRefrH) * uRefrA;
          vec2 off = d * grad * uLayerScale;
          vec2 base = px + off;
          vec2 disp = off * ((local.x * local.y) / (kh.x * kh.y));
          vec3 sR = backdrop(base + disp);
          vec3 sO = backdrop(base + disp * (2.0 / 3.0));
          vec3 sY = backdrop(base + disp * (1.0 / 3.0));
          vec3 sG = backdrop(base);
          vec3 sC = backdrop(base - disp * (1.0 / 3.0));
          vec3 sB = backdrop(base - disp * (2.0 / 3.0));
          vec3 sP = backdrop(base - disp);
          // Channel weights from the original AGSL shader.
          kc = vec3(
            (sR.r + sO.r + sY.r) / 3.5 + sP.r / 7.0,
            sO.g / 7.0 + (sY.g + sG.g + sC.g) / 3.5,
            (sC.b + sB.b + sP.b) / 3.0
          );
        }

        // Inner shadow — radius 4dp x press, offset (0, radius).
        if (uInnerA > 0.001) {
          float sdI = sdRoundedRect((px - uKnobC - uInnerOff) / uLayerScale, kh, r) * minScale;
          float ia = uInnerA * smoothstep(-uInnerR, uInnerR, sdI);
          kc = mix(kc, vec3(0.0), ia);
        }

        // White pebble surface, fading out while pressed.
        kc = mix(kc, vec3(1.0), 1.0 - uPress);

        // Ambient rim highlight at 45 degrees: bright on one side, dimming on the other.
        if (uHlAlpha > 0.001) {
          float band = 1.0 - smoothstep(0.0, uHlWidth, -sdK * minScale);
          float dd = dot(grad, vec2(0.70710678, 0.70710678));
          float i = abs(dd) * band * uHlAlpha;
          kc = kc * (1.0 - i) + vec3(step(0.0, dd) * i);
        }

        col = over(col, vec4(kc * cov, cov));
      }
      gl_FragColor = col;
    }
  `;

  let sharedGL = null;
  let glFailed = false;

  function initSharedGL() {
    if (sharedGL || glFailed) return sharedGL;
    const canvas = document.createElement('canvas');
    canvas.width = OFFSCREEN_W;
    canvas.height = OFFSCREEN_H;
    const opts = { alpha: true, antialias: false, premultipliedAlpha: true, preserveDrawingBuffer: true };
    const gl = canvas.getContext('webgl', opts) || canvas.getContext('experimental-webgl', opts);
    if (!gl) { glFailed = true; return null; }
    const compile = (type, src) => {
      const s = gl.createShader(type);
      gl.shaderSource(s, src);
      gl.compileShader(s);
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) {
        console.warn('[liquid-toggle] shader compile failed:', gl.getShaderInfoLog(s));
        return null;
      }
      return s;
    };
    const v = compile(gl.VERTEX_SHADER, VERT_SRC);
    const f = compile(gl.FRAGMENT_SHADER, FRAG_SRC);
    if (!v || !f) { glFailed = true; return null; }
    const prog = gl.createProgram();
    gl.attachShader(prog, v);
    gl.attachShader(prog, f);
    gl.linkProgram(prog);
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) {
      console.warn('[liquid-toggle] link failed:', gl.getProgramInfoLog(prog));
      glFailed = true;
      return null;
    }
    const quad = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW);
    const u = {};
    const count = gl.getProgramParameter(prog, gl.ACTIVE_UNIFORMS);
    for (let i = 0; i < count; i++) {
      const info = gl.getActiveUniform(prog, i);
      u[info.name] = gl.getUniformLocation(prog, info.name);
    }
    sharedGL = { canvas, gl, prog, quad, u, aPos: gl.getAttribLocation(prog, 'aPos') };
    return sharedGL;
  }

  /* ---------------------------------------------------------------- *
   * Colours (palettes.ts: toggleAccent / toggleTrackOff).
   * ---------------------------------------------------------------- */
  function palette() {
    return isDark()
      ? { off: [120 / 255, 120 / 255, 128 / 255, 0.36], on: [0x30 / 255, 0xd1 / 255, 0x58 / 255, 1], base: [0.11, 0.11, 0.118] }
      : { off: [120 / 255, 120 / 255, 120 / 255, 0.2], on: [0x34 / 255, 0xc7 / 255, 0x59 / 255, 1], base: [0.965, 0.968, 0.976] };
  }

  function parseColor(value) {
    const m = /rgba?\(([^)]+)\)/.exec(value || '');
    if (!m) return null;
    const p = m[1].split(/[\s,/]+/).filter(Boolean).map(Number);
    return [p[0] / 255, p[1] / 255, p[2] / 255, p.length > 3 ? p[3] : 1];
  }

  // The lens refracts whatever sits behind the switch. Inside m-ui that is
  // a card, so composite the ancestors' background colours (nearest first)
  // over the theme base — the CanvasBackdrop(solid colour) case of the original.
  function resolveBackdrop(shell, base) {
    const layers = [];
    let coverage = 0;
    for (let el = shell.parentElement; el && coverage < 0.995; el = el.parentElement) {
      const c = parseColor(getComputedStyle(el).backgroundColor);
      if (c && c[3] > 0.01) {
        layers.push(c);
        coverage = 1 - (1 - coverage) * (1 - c[3]);
      }
    }
    let rgb = base.slice();
    for (let i = layers.length - 1; i >= 0; i--) {
      const c = layers[i];
      rgb = [rgb[0] + (c[0] - rgb[0]) * c[3], rgb[1] + (c[1] - rgb[1]) * c[3], rgb[2] + (c[2] - rgb[2]) * c[3]];
    }
    return rgb;
  }

  /* ---------------------------------------------------------------- *
   * Controllers
   * ---------------------------------------------------------------- */
  const controllers = new Map();
  let raf = 0;
  let prevT = 0;

  function wake() {
    if (!raf) {
      prevT = 0;
      raf = requestAnimationFrame(loop);
    }
  }

  function getChecked(ctrl) {
    const el = ctrl.input;
    if (!el) return false;
    if (el.matches && el.matches('input')) return el.checked;
    return !!(el.classList && el.classList.contains('status-mode'));
  }

  function isDisabled(ctrl) {
    const el = ctrl.input;
    if (!el) return false;
    return (el.matches && el.matches(':disabled')) || el.getAttribute('aria-disabled') === 'true';
  }

  // Returns false while the switch is not laid out (e.g. inside a closed
  // menu): measuring then would fall back to a default size and the switch
  // would later be drawn at the wrong size over its neighbours. The per-switch
  // ResizeObserver re-measures as soon as it gets a real box.
  function measure(ctrl) {
    const shell = ctrl.shell;
    const tw = shell.offsetWidth;
    const th = shell.offsetHeight;
    if (!tw || !th) return false;
    const s = th / 28; // one iOS dp in CSS px for this switch size
    ctrl.trackW = tw;
    ctrl.trackH = th;
    ctrl.s = s;
    ctrl.knobW = tw * 40 / 64;
    ctrl.knobH = th * 24 / 28;
    ctrl.pad = 2 * s;
    ctrl.travel = Math.max(1, tw - ctrl.knobW - 2 * ctrl.pad);
    // Room for the 1.5x lens plus a maximal (1/0.8) stretch and the shadow.
    ctrl.margin = Math.ceil(Math.max(ctrl.knobW * (PRESSED_SCALE * 1.25 - 1) / 2 + 4 * s, (ctrl.knobH * PRESSED_SCALE - th) / 2 + 6 * s) + 2);
    ctrl.rtl = isRtl(shell);
    ctrl.colors = palette();
    ctrl.backdrop = resolveBackdrop(shell, ctrl.colors.base);
    ctrl.canvas.style.setProperty('top', -ctrl.margin + 'px', 'important');
    ctrl.canvas.style.setProperty('left', -ctrl.margin + 'px', 'important');
    return true;
  }

  function draw(ctrl) {
    const shell = ctrl.shell;
    if (!shell.isConnected) return;
    if (!ctrl.trackW && !measure(ctrl)) return;
    const { trackW: tw, trackH: th, margin, s } = ctrl;
    const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
    const totalW = tw + margin * 2;
    const totalH = th + margin * 2;
    const W = Math.min(OFFSCREEN_W, Math.round(totalW * dpr));
    const H = Math.min(OFFSCREEN_H, Math.round(totalH * dpr));
    const canvas = ctrl.canvas;
    if (canvas.width !== W || canvas.height !== H) {
      canvas.width = W;
      canvas.height = H;
      canvas.style.width = totalW + 'px';
      canvas.style.height = totalH + 'px';
    }

    const c = ctrl.colors;
    const f = ctrl.fraction;
    const trackColor = c.off.map((v, i) => v + (c.on[i] - v) * f);
    const p = ctrl.press;

    // Knob transform: spring scale x velocity squash (divisor 50).
    const vel = ctrl.velocity / 50;
    const velX = Math.max(-0.2, Math.min(0.2, vel * 0.75));
    const velY = Math.max(-0.2, Math.min(0.2, vel * 0.25));
    const scaleX = ctrl.scaleX / (1 - velX);
    const scaleY = ctrl.scaleY * (1 - velY);
    const visual = ctrl.rtl ? 1 - f : f;
    const knobCX = margin + ctrl.pad + ctrl.knobW / 2 + visual * ctrl.travel;
    const knobCY = margin + th / 2;
    const trackCX = margin + tw / 2;
    const trackCY = margin + th / 2;

    const sGL = initSharedGL();
    const ctx2d = ctrl.ctx;
    if (!sGL) {
      // No WebGL: flat iOS switch.
      ctx2d.setTransform(1, 0, 0, 1, 0, 0);
      ctx2d.clearRect(0, 0, W, H);
      ctx2d.scale(dpr, dpr);
      ctx2d.fillStyle = `rgba(${trackColor.slice(0, 3).map(v => Math.round(v * 255)).join(',')},${trackColor[3]})`;
      ctx2d.beginPath();
      ctx2d.roundRect(margin, margin, tw, th, th / 2);
      ctx2d.fill();
      ctx2d.fillStyle = '#fff';
      ctx2d.shadowColor = 'rgba(0,0,0,.12)';
      ctx2d.shadowBlur = 4 * s;
      ctx2d.beginPath();
      ctx2d.roundRect(knobCX - ctrl.knobW / 2, knobCY - ctrl.knobH / 2, ctrl.knobW, ctrl.knobH, ctrl.knobH / 2);
      ctx2d.fill();
      ctx2d.setTransform(1, 0, 0, 1, 0, 0);
      return;
    }

    const { gl, prog, quad, u, aPos } = sGL;
    gl.useProgram(prog);
    gl.viewport(0, OFFSCREEN_H - H, W, H);
    gl.scissor(0, OFFSCREEN_H - H, W, H);
    gl.enable(gl.SCISSOR_TEST);
    gl.clearColor(0, 0, 0, 0);
    gl.clear(gl.COLOR_BUFFER_BIT);
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.enableVertexAttribArray(aPos);
    gl.vertexAttribPointer(aPos, 2, gl.FLOAT, false, 0, 0);

    const k = dpr;
    // Scaled track copy seen through the knob: X lerp(2/3, 0.75), Y lerp(0, 0.75), pivot = knob centre.
    const tsx = 2 / 3 + (0.75 - 2 / 3) * p;
    const tsy = 0.75 * p;
    gl.uniform1f(u.uTop, OFFSCREEN_H);
    gl.uniform2f(u.uTrackC, trackCX * k, trackCY * k);
    gl.uniform2f(u.uTrackHalf, tw / 2 * k, th / 2 * k);
    gl.uniform4f(u.uTrackColor, trackColor[0], trackColor[1], trackColor[2], trackColor[3]);
    gl.uniform3f(u.uBackdrop, ctrl.backdrop[0], ctrl.backdrop[1], ctrl.backdrop[2]);
    gl.uniform2f(u.uKnobC, knobCX * k, knobCY * k);
    gl.uniform2f(u.uKnobHalf, ctrl.knobW / 2 * k, ctrl.knobH / 2 * k);
    gl.uniform2f(u.uLayerScale, scaleX, scaleY);
    gl.uniform1f(u.uPress, p);
    gl.uniform1f(u.uRefrH, 5 * s * p * k);
    gl.uniform1f(u.uRefrA, -10 * s * p * k);
    gl.uniform1f(u.uBlur, 8 * s * (1 - p) * k);
    gl.uniform2f(u.uSTrackC, (knobCX + (trackCX - knobCX) * tsx) * k, (knobCY + (trackCY - knobCY) * tsy) * k);
    gl.uniform2f(u.uSTrackHalf, tw * tsx / 2 * k, th * tsy / 2 * k);
    gl.uniform1f(u.uSTrackR, th / 2 * Math.min(tsx, tsy) * k);
    gl.uniform1f(u.uShadowR, 4 * s * k);
    gl.uniform1f(u.uShadowA, isDark() ? 0.12 : 0.05);
    gl.uniform2f(u.uShadowOff, 0, 4 / 6 * s * k);
    gl.uniform1f(u.uInnerR, Math.max(0.001, 4 * s * p * k));
    gl.uniform1f(u.uInnerA, 0.3 * p);
    gl.uniform2f(u.uInnerOff, 0, 4 * s * p * k);
    gl.uniform1f(u.uHlWidth, Math.max(1, (0.5 / 1.5 + 0.25 / 1.5) * s * k));
    gl.uniform1f(u.uHlAlpha, p);
    gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);

    ctx2d.clearRect(0, 0, W, H);
    ctx2d.drawImage(sGL.canvas, 0, 0, W, H, 0, 0, W, H);
  }

  /* ---------------------------------------------------------------- *
   * Animation API (methods-toggle.ts)
   * ---------------------------------------------------------------- */
  function press(ctrl) {
    if (reducedMotion.matches) return;
    ctrl.targetPress = 1;
    ctrl.targetScaleX = PRESSED_SCALE;
    ctrl.targetScaleY = PRESSED_SCALE;
  }

  // Tap / keyboard / programmatic toggle: animateToValue = press + animateTo + release.
  function setTarget(ctrl, target) {
    if (ctrl.dragging || ctrl.targetFraction === target) return;
    ctrl.targetFraction = target;
    ctrl.trackVelocity = false;
    ctrl.velocity = 0;
    ctrl.velocityV = 0;
    ctrl.targetVelocity = 0;
    ctrl.samples.length = 0;
    if (ctrl.targetPress === 0) press(ctrl);
    wake();
  }

  function snapTo(ctrl, target) {
    Object.assign(ctrl, {
      fraction: target, fractionV: 0, targetFraction: target,
      press: 0, pressV: 0, targetPress: 0,
      scaleX: 1, scaleXV: 0, targetScaleX: 1,
      scaleY: 1, scaleYV: 0, targetScaleY: 1,
      velocity: 0, velocityV: 0, targetVelocity: 0
    });
    ctrl.samples.length = 0;
  }

  function stepController(ctrl, dt, now) {
    let active = false;
    // Auto-release once the value has (nearly) settled — but never while a
    // finger or mouse button is still down on the switch (held), otherwise
    // the lens collapsed on the first frame of a press-and-drag.
    if (ctrl.targetPress === 1 && !ctrl.held && !ctrl.dragging && Math.abs(ctrl.targetFraction - ctrl.fraction) < 0.02) {
      ctrl.targetPress = 0;
      ctrl.targetScaleX = 1;
      ctrl.targetScaleY = 1;
    }
    if (Math.abs(ctrl.targetFraction - ctrl.fraction) > THRESHOLD || Math.abs(ctrl.fractionV) > THRESHOLD) {
      [ctrl.fraction, ctrl.fractionV] = springCritical(ctrl.fraction, ctrl.fractionV, ctrl.targetFraction, dt, VALUE_OMEGA);
      if (ctrl.trackVelocity || ctrl.dragging) {
        ctrl.samples.push({ t: now, p: ctrl.fraction });
        if (ctrl.samples.length > 20) ctrl.samples.shift();
        ctrl.targetVelocity = trackerVelocity(ctrl.samples);
      }
      active = true;
    } else {
      ctrl.fraction = ctrl.targetFraction;
      ctrl.fractionV = 0;
      if (!ctrl.dragging) {
        ctrl.targetVelocity = 0;
        ctrl.trackVelocity = false;
        ctrl.samples.length = 0;
      }
    }
    const springs = [
      ['press', 'pressV', 'targetPress', null],
      ['scaleX', 'scaleXV', 'targetScaleX', SCALE_X],
      ['scaleY', 'scaleYV', 'targetScaleY', SCALE_Y],
      ['velocity', 'velocityV', 'targetVelocity', VELOCITY]
    ];
    for (const [key, vKey, tKey, spec] of springs) {
      if (Math.abs(ctrl[tKey] - ctrl[key]) > THRESHOLD || Math.abs(ctrl[vKey]) > THRESHOLD) {
        [ctrl[key], ctrl[vKey]] = spec
          ? springUnder(ctrl[key], ctrl[vKey], ctrl[tKey], dt, spec.omega, spec.zeta)
          : springCritical(ctrl[key], ctrl[vKey], ctrl[tKey], dt, VALUE_OMEGA);
        active = true;
      } else {
        ctrl[key] = ctrl[tKey];
        ctrl[vKey] = 0;
      }
    }
    ctrl.press = Math.max(0, Math.min(1, ctrl.press));
    return active || ctrl.dragging || ctrl.targetPress === 1;
  }

  function loop(now) {
    raf = 0;
    const dt = Math.min((now - (prevT || now - 16)) / 1000, 0.032);
    prevT = now;
    let anyActive = false;
    for (const ctrl of controllers.values()) {
      if (!ctrl.shell.isConnected) {
        ctrl.ro.disconnect();
        controllers.delete(ctrl.shell);
        continue;
      }
      const active = stepController(ctrl, dt, now);
      if (active || ctrl.needsDraw) {
        draw(ctrl);
        ctrl.needsDraw = false;
      }
      if (active) anyActive = true;
    }
    if (anyActive) raf = requestAnimationFrame(loop);
    else prevT = 0;
  }

  function createController(shell, input) {
    if (controllers.has(shell)) return controllers.get(shell);
    shell.classList.add('lg-toggle');
    if (shell.classList.contains('switch-sm')) shell.classList.add('lt-small');
    let canvas = shell.querySelector(':scope > .liquid-toggle-canvas');
    if (!canvas) {
      canvas = document.createElement('canvas');
      canvas.className = 'liquid-toggle-canvas';
      canvas.setAttribute('aria-hidden', 'true');
      shell.prepend(canvas);
    }
    const start = input && (input.matches('input') ? input.checked : input.classList.contains('status-mode')) ? 1 : 0;
    const ctrl = { shell, input, canvas, ctx: canvas.getContext('2d'), samples: [], dragging: false, needsDraw: true };
    snapTo(ctrl, start);
    controllers.set(shell, ctrl);
    ctrl.ro = new ResizeObserver(() => {
      if (measure(ctrl)) { ctrl.needsDraw = true; wake(); }
    });
    ctrl.ro.observe(shell);
    measure(ctrl);
    draw(ctrl);
    return ctrl;
  }

  function upgrade(input) {
    if (!input || input.hidden || input.style.display === 'none') return;
    let shell = input.parentElement;
    if (shell.matches('.check')) {
      shell = document.createElement('span');
      input.before(shell);
      shell.append(input);
    }
    input.setAttribute('role', 'switch');
    createController(shell, input);
  }

  function scan() {
    document.querySelectorAll(SELECTOR).forEach(upgrade);
    document.querySelectorAll('button.search-toggle').forEach(btn => {
      btn.setAttribute('role', 'switch');
      const checked = String(btn.classList.contains('status-mode'));
      if (btn.getAttribute('aria-checked') !== checked) btn.setAttribute('aria-checked', checked);
      createController(btn, btn);
    });
  }

  /* ---------------------------------------------------------------- *
   * Pointer gestures: press on down, relative drag, snap on release.
   * ---------------------------------------------------------------- */
  let activePointer = null;
  let suppressClick = null;

  function onPointerDown(e) {
    if (e.button !== 0 || !e.isPrimary) return;
    const shell = e.target.closest('.lg-toggle');
    const ctrl = shell && controllers.get(shell);
    if (!ctrl || isDisabled(ctrl)) return;
    measure(ctrl);
    activePointer = { id: e.pointerId, ctrl, startX: e.clientX, startY: e.clientY, startFraction: ctrl.targetFraction, dragged: false };
    ctrl.held = true;
    press(ctrl);
    wake();
  }

  function onPointerMove(e) {
    const p = activePointer;
    if (!p || e.pointerId !== p.id) return;
    const ctrl = p.ctrl;
    const dx = e.clientX - p.startX;
    const dy = e.clientY - p.startY;
    if (!p.dragged) {
      if (Math.abs(dy) > 6 && Math.abs(dy) > Math.abs(dx)) { onPointerUp(e, true); return; }
      if (Math.abs(dx) < 3) return;
      p.dragged = true;
      ctrl.dragging = true;
      press(ctrl);
      ctrl.samples.length = 0;
      ctrl.velocity = ctrl.velocityV = ctrl.targetVelocity = 0;
      if (ctrl.shell.setPointerCapture) {
        try { ctrl.shell.setPointerCapture(e.pointerId); } catch (_) {}
      }
    }
    if (e.cancelable) e.preventDefault();
    const delta = (ctrl.rtl ? -dx : dx) / ctrl.travel;
    ctrl.targetFraction = Math.max(0, Math.min(1, p.startFraction + delta));
    wake();
  }

  function onPointerUp(e, cancel) {
    const p = activePointer;
    if (!p || (e && e.pointerId !== p.id)) return;
    activePointer = null;
    const ctrl = p.ctrl;
    ctrl.held = false;
    if (ctrl.shell.releasePointerCapture) {
      try { ctrl.shell.releasePointerCapture(p.id); } catch (_) {}
    }
    if (!p.dragged) {
      // A plain tap: the native click → change path moves the value; if
      // nothing changes (cancel, disabled) the press auto-releases.
      wake();
      return;
    }
    suppressClick = { shell: ctrl.shell, until: performance.now() + 400 };
    ctrl.dragging = false;
    const current = getChecked(ctrl);
    const next = cancel ? current : ctrl.targetFraction >= 0.5;
    ctrl.targetFraction = next ? 1 : 0;
    ctrl.trackVelocity = true;
    if (next !== current && !isDisabled(ctrl) && ctrl.input && ctrl.input.isConnected) ctrl.input.click();
    wake();
  }

  function sync(ctrl) {
    setTarget(ctrl, getChecked(ctrl) ? 1 : 0);
  }

  function init() {
    scan();
    document.addEventListener('pointerdown', onPointerDown, { passive: true });
    window.addEventListener('pointermove', onPointerMove, { passive: false });
    window.addEventListener('pointerup', e => onPointerUp(e, false), { passive: true });
    window.addEventListener('pointercancel', e => onPointerUp(e, true), { passive: true });

    document.addEventListener('click', e => {
      if (e.isTrusted && e.detail !== 0 && suppressClick && performance.now() < suppressClick.until && suppressClick.shell.contains(e.target)) {
        e.preventDefault();
        e.stopImmediatePropagation();
        return;
      }
      const shell = e.target.closest && e.target.closest('.lg-toggle');
      const ctrl = shell && controllers.get(shell);
      if (!ctrl || isDisabled(ctrl)) return;
      // Buttons (search-toggle) have no change event.
      if (!ctrl.input || !ctrl.input.matches || !ctrl.input.matches('input')) requestAnimationFrame(() => sync(ctrl));
    }, true);

    document.addEventListener('change', e => {
      if (!e.target.matches || !e.target.matches('input')) return;
      const shell = e.target.closest('.lg-toggle');
      const ctrl = shell && controllers.get(shell);
      if (ctrl) sync(ctrl);
    }, true);

    document.addEventListener('reset', e => {
      requestAnimationFrame(() => {
        controllers.forEach(ctrl => {
          if (ctrl.input && ctrl.input.form === e.target) {
            snapTo(ctrl, getChecked(ctrl) ? 1 : 0);
            ctrl.needsDraw = true;
          }
        });
        wake();
      });
    });

    const refreshAll = () => {
      controllers.forEach(ctrl => { measure(ctrl); ctrl.needsDraw = true; });
      wake();
    };

    new MutationObserver(records => {
      let needsScan = false;
      let themeChanged = false;
      for (const r of records) {
        if (r.type === 'childList') { needsScan = true; continue; }
        if ((r.target === document.body || r.target === document.documentElement) && (r.attributeName === 'class' || r.attributeName === 'data-theme' || r.attributeName === 'dir')) themeChanged = true;
        if (r.attributeName === 'class' && r.target.matches && r.target.matches('button.search-toggle.lg-toggle')) {
          const checked = String(r.target.classList.contains('status-mode'));
          if (r.target.getAttribute('aria-checked') !== checked) r.target.setAttribute('aria-checked', checked);
          const ctrl = controllers.get(r.target);
          if (ctrl) sync(ctrl);
        }
      }
      if (needsScan) {
        controllers.forEach((ctrl, shell) => { if (!shell.isConnected) { ctrl.ro.disconnect(); controllers.delete(shell); } });
        scan();
      }
      if (themeChanged) refreshAll();
    }).observe(document.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'data-theme', 'dir'] });

    // Programmatic `input.checked = x` fires no event; catch it cheaply.
    setInterval(() => {
      controllers.forEach(ctrl => {
        if (ctrl.dragging || !ctrl.shell.isConnected) return;
        const want = getChecked(ctrl) ? 1 : 0;
        if (ctrl.targetFraction !== want) {
          if (document.hidden || !ctrl.shell.offsetParent) snapTo(ctrl, want), ctrl.needsDraw = true;
          else setTarget(ctrl, want);
          wake();
        }
      });
    }, 250);

    window.addEventListener('resize', refreshAll, { passive: true });
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init, { once: true });
  else init();

  window.MUILiquidToggle = { controllers, draw, measure, setTarget };
})();
