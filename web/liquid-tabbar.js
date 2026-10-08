/* m-ui Liquid Glass tab bar (.settings-tabbar, .mihomo-tabs, ...)
 *
 * Faithful to LiquidBottomTabs.kt as shown by the reference demo: at rest the
 * selected tab sits under a quiet dim capsule (black 10% light / white 10%
 * dark) and only the content that capsule covers is tinted with the accent
 * colour. While pressed or dragged the capsule grows into a clear glass lens.
 *
 * Motion and optics are ported from liquid-glass-webgl
 * (https://github.com/martin65536/liquid-glass-webgl, Apache-2.0, a web port
 * of Kyant0/AndroidLiquidGlass): the LiquidBottomTabs indicator animation
 * (DampedDragAnimation with pressedScale 78/56, velocity squash divisor 10,
 * tab content scale lerp(1, 1.2), container scale lerp(1, 1 + 16dp / width)),
 * the lens refraction (height 10dp, amount -14dp, 7-path dispersion), inner
 * shadow and the Default rim highlight. See NOTICE.
 *
 * Labels stay real DOM text on top of the canvas, so translations, focus and
 * screen readers are untouched. The canvas only draws the track and thumb.
 */
(() => {
  'use strict';
  if (window.MUILiquidTabBar) return;

  const BAR_SELECTOR = '.liquid-tabbar, .settings-tabbar, .mihomo-tabs, .inbound-status-tabs, .mihomo-editor-tabs';
  const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');

  const isDark = () => document.body.classList.contains('dark') ||
    document.documentElement.classList.contains('dark') ||
    document.documentElement.getAttribute('data-theme') === 'dark' ||
    document.documentElement.getAttribute('data-theme') === 'ultra-dark';

  /* ---------------- springs (renderer/spring.ts) ---------------- */
  const THRESHOLD = 0.003;
  const VALUE_OMEGA = Math.sqrt(1000);
  const SCALE_X = { omega: Math.sqrt(250), zeta: 0.6 };
  const SCALE_Y = { omega: Math.sqrt(250), zeta: 0.7 };
  const VELOCITY = { omega: Math.sqrt(300), zeta: 0.5 };
  const PRESSED_SCALE = 78 / 56;
  const LENS_GAIN = 1;

  function springCritical(x, v, target, dt, omega) {
    const x0 = x - target;
    const decay = Math.exp(-omega * dt);
    const offset = x0 * decay + (v + omega * x0) * dt * decay;
    return [target + offset, -omega * x0 * decay + (v + omega * x0) * (decay - omega * dt * decay)];
  }
  function springUnder(x, v, target, dt, omega, zeta) {
    const x0 = x - target;
    const omegaD = omega * Math.sqrt(1 - zeta * zeta);
    const decay = Math.exp(-zeta * omega * dt);
    const cos = Math.cos(omegaD * dt), sin = Math.sin(omegaD * dt);
    const b0 = (v + zeta * omega * x0) / omegaD;
    const offset = x0 * decay * cos + b0 * decay * sin;
    return [target + offset, -zeta * omega * offset + decay * (-x0 * omegaD * sin + b0 * omegaD * cos)];
  }
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

  /* ---------------- shader ---------------- */
  const OFFSCREEN_W = 2048;
  const OFFSCREEN_H = 256;
  const VERT_SRC = 'attribute vec2 aPos; void main(){ gl_Position = vec4(aPos, 0.0, 1.0); }';
  const FRAG_SRC = `
    precision highp float;
    uniform float uTop;
    uniform vec2  uTrackC;
    uniform vec2  uTrackHalf;
    uniform vec4  uTrackColor;
    uniform vec3  uBackdrop;
    uniform vec4  uDim;            // indicator surface at rest (drawRect(dimColor 0.1), alpha 1 - press)
    uniform vec2  uPlateHalf;      // inner backdrop plate: rest-height capsule across the bar
    uniform vec2  uThumbC;
    uniform vec2  uThumbHalf;      // unscaled
    uniform vec2  uLayerScale;
    uniform float uPress;
    uniform float uRefrH;
    uniform float uRefrA;
    uniform float uShadowR;
    uniform float uShadowA;
    uniform vec2  uShadowOff;
    uniform float uInnerR;
    uniform float uInnerA;
    uniform vec2  uInnerOff;
    uniform float uHlWidth;
    uniform float uHlAlpha;
    uniform float uMode;           // 0: back layer (track + thumb shadow), 1: front layer (thumb + labels)
    uniform sampler2D uLabels;     // premultiplied raster of the segment labels, same frame as the canvas
    uniform vec2  uCanvas;
    uniform float uPlateShadowSigma; // inner backdrop plate (the track) as seen through the lens
    uniform float uPlateShadowOff;
    uniform float uPlateShadowA;
    uniform float uPlateHlW;

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

    // What the lens sees — the reference's CombinedBackdrop for the tab
    // indicator (sampleIndicatorBackdrop). The "inner backdrop plate" is the
    // hidden Row: a capsule as tall as the resting indicator, spanning the
    // bar inside its padding, with Shadow.Default below it and a
    // Highlight.Default rim, both faded in with the press progress. Outside
    // the plate the lens sees what is behind the bar (the card). Once pressed
    // the lens is much taller than the plate, so the plate's top and bottom
    // rims fall into the refraction band and show as the two bent bright
    // lines of the reference.
    vec3 backdrop(vec2 px) {
      vec2 pl = px - uTrackC;
      float r = uPlateHalf.y;
      float sd = sdRoundedRect(pl, uPlateHalf, r);
      float sds = sdRoundedRect(pl - vec2(0.0, uPlateShadowOff), uPlateHalf, r);
      float shadow = exp(-sds * sds / (2.0 * uPlateShadowSigma * uPlateShadowSigma)) * smoothstep(-1.0, 1.0, sd);
      // At rest the indicator just sees the track; pressed, the track tint is
      // what the plate carries (outside the plate the card shows through).
      float m = clamp(0.5 - sd, 0.0, 1.0);
      float mt = clamp(0.5 - sdRoundedRect(pl, uTrackHalf, uTrackHalf.y), 0.0, 1.0);
      vec3 c = uBackdrop * (1.0 - shadow * uPlateShadowA * uPress);
      c = mix(c, uTrackColor.rgb, uTrackColor.a * mix(mt, m, uPress));
      vec2 g = gradSdRoundedRect(pl, uPlateHalf, min(r * 1.5, min(uPlateHalf.x, uPlateHalf.y)));
      float band = (1.0 - smoothstep(0.0, uPlateHlW, -sd)) * m;
      return min(c + vec3(abs(dot(g, vec2(0.70710678, 0.70710678))) * band * uPress), vec3(1.0));
    }

    // Everything the thumb shows at px: the thumb surface (opaque at rest,
    // clear when pressed) over the card/track, with the labels on top — the
    // labels are what the lens visibly bends and splits into colour.
    // lw fades the labels out towards the rim: bending whole glyphs into the
    // steep outer band smeared them into dark fragments along the edge.
    vec3 scene(vec2 px, float lw) {
      // onDrawSurface: drawRect(dimColor 0.1, alpha = 1 - press) + drawRect(Black 3% x press).
      vec3 c = mix(backdrop(px), uDim.rgb, uDim.a * (1.0 - uPress)) * (1.0 - 0.03 * uPress);
      vec4 l = texture2D(uLabels, clamp(px / uCanvas, 0.0, 1.0)) * lw;
      return l.rgb + c * (1.0 - l.a);
    }

    void main() {
      vec2 px = vec2(gl_FragCoord.x, uTop - gl_FragCoord.y);
      vec4 col = vec4(0.0);
      if (uMode < 0.5) {
        float sdT = sdRoundedRect(px - uTrackC, uTrackHalf, uTrackHalf.y);
        float aT = clamp(0.5 - sdT, 0.0, 1.0) * uTrackColor.a;
        col = vec4(uTrackColor.rgb * aT, aT);
      }

      vec2 th = uThumbHalf;
      float r = min(th.x, th.y);
      float minScale = min(uLayerScale.x, uLayerScale.y);
      vec2 local = (px - uThumbC) / uLayerScale;
      float sdK = sdRoundedRect(local, th, r);

      if (uMode < 0.5) {
        float sdS = sdRoundedRect((px - uThumbC - uShadowOff) / uLayerScale, th, r) * minScale;
        gl_FragColor = over(col, vec4(0.0, 0.0, 0.0, uShadowA * (1.0 - smoothstep(-uShadowR, uShadowR, sdS))));
        return;
      }

      float cov = clamp(0.5 - sdK * minScale, 0.0, 1.0);
      if (cov > 0.0) {
        float rim = 0.0;
        if (uRefrH > 0.5 && (-sdK) < uRefrH) rim = circleMap(1.0 - (-min(sdK, 0.0)) / uRefrH);
        float lw = 1.0 - smoothstep(0.18, 0.6, rim);
        vec3 kc = scene(px, lw);
        vec2 grad = gradSdRoundedRect(local, th, min(r * 1.5, min(th.x, th.y)));
        if (uRefrH > 0.5 && (-sdK) < uRefrH) {
          float d = circleMap(1.0 - (-min(sdK, 0.0)) / uRefrH) * uRefrA;
          vec2 off = d * grad * uLayerScale;
          vec2 base = px + off;
          vec2 disp = off * ((local.x * local.y) / (th.x * th.y));
          vec3 sR = scene(base + disp, lw);
          vec3 sO = scene(base + disp * (2.0 / 3.0), lw);
          vec3 sY = scene(base + disp * (1.0 / 3.0), lw);
          vec3 sG = scene(base, lw);
          vec3 sC = scene(base - disp * (1.0 / 3.0), lw);
          vec3 sB = scene(base - disp * (2.0 / 3.0), lw);
          vec3 sP = scene(base - disp, lw);
          kc = vec3((sR.r + sO.r + sY.r) / 3.5 + sP.r / 7.0,
                    sO.g / 7.0 + (sY.g + sG.g + sC.g) / 3.5,
                    (sC.b + sB.b + sP.b) / 3.0);
        }
        if (uInnerA > 0.001) {
          float sdI = sdRoundedRect((px - uThumbC - uInnerOff) / uLayerScale, th, r) * minScale;
          kc = mix(kc, vec3(0.0), uInnerA * smoothstep(-uInnerR, uInnerR, sdI));
        }
        // Ambient rim highlight, as on the switch knob: bright on one side and
        // dimming on the other, so the glass edge reads on a white card too
        // (the reference's additive Default rim is invisible on white).
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

  let sharedGL = null, glFailed = false;
  function initGL() {
    if (sharedGL || glFailed) return sharedGL;
    const canvas = document.createElement('canvas');
    canvas.width = OFFSCREEN_W;
    canvas.height = OFFSCREEN_H;
    const gl = canvas.getContext('webgl', { alpha: true, antialias: false, premultipliedAlpha: true, preserveDrawingBuffer: true });
    if (!gl) { glFailed = true; return null; }
    const compile = (type, src) => {
      const s = gl.createShader(type);
      gl.shaderSource(s, src);
      gl.compileShader(s);
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) { console.warn('[liquid-tabbar] shader:', gl.getShaderInfoLog(s)); return null; }
      return s;
    };
    const v = compile(gl.VERTEX_SHADER, VERT_SRC), f = compile(gl.FRAGMENT_SHADER, FRAG_SRC);
    if (!v || !f) { glFailed = true; return null; }
    const prog = gl.createProgram();
    gl.attachShader(prog, v);
    gl.attachShader(prog, f);
    gl.linkProgram(prog);
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) { glFailed = true; return null; }
    const quad = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW);
    const u = {};
    for (let i = 0, n = gl.getProgramParameter(prog, gl.ACTIVE_UNIFORMS); i < n; i++) {
      const info = gl.getActiveUniform(prog, i);
      u[info.name] = gl.getUniformLocation(prog, info.name);
    }
    const labelTex = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, labelTex);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    sharedGL = { canvas, gl, prog, quad, u, labelTex, aPos: gl.getAttribLocation(prog, 'aPos') };
    return sharedGL;
  }

  /* ---------------- colours ---------------- */
  // Apple tertiarySystemFill track; white thumb (light) / systemGray3 thumb (dark).
  function palette() {
    return isDark()
      ? { track: [118 / 255, 118 / 255, 128 / 255, 0.24], dim: [1, 1, 1, 0.1], accent: '#0091ff', base: [0.11, 0.11, 0.118], shadow: 0.2 }
      : { track: [118 / 255, 118 / 255, 128 / 255, 0.12], dim: [0, 0, 0, 0.1], accent: '#0088ff', base: [0.965, 0.968, 0.976], shadow: 0.1 };
  }
  function parseColor(value) {
    const m = /rgba?\(([^)]+)\)/.exec(value || '');
    if (!m) return null;
    const p = m[1].split(/[\s,/]+/).filter(Boolean).map(Number);
    return [p[0] / 255, p[1] / 255, p[2] / 255, p.length > 3 ? p[3] : 1];
  }
  function resolveBackdrop(el, base) {
    const layers = [];
    let coverage = 0;
    for (let n = el.parentElement; n && coverage < 0.995; n = n.parentElement) {
      const c = parseColor(getComputedStyle(n).backgroundColor);
      if (c && c[3] > 0.01) { layers.push(c); coverage = 1 - (1 - coverage) * (1 - c[3]); }
    }
    let rgb = base.slice();
    for (let i = layers.length - 1; i >= 0; i--) {
      const c = layers[i];
      rgb = rgb.map((v, k) => v + (c[k] - v) * c[3]);
    }
    return rgb;
  }

  /* ---------------- controllers ---------------- */
  const controllers = new Map();
  let raf = 0, prevT = 0;
  function wake() { if (!raf) { prevT = 0; raf = requestAnimationFrame(loop); } }

  const buttonsOf = bar => Array.from(bar.querySelectorAll(':scope > button')).filter(b => !b.hidden && b.offsetWidth > 0);
  const isActive = b => b.classList.contains('active') || b.getAttribute('aria-selected') === 'true';
  function activeIndex(ctrl) {
    const i = ctrl.buttons.findIndex(isActive);
    return i < 0 ? 0 : i;
  }

  function measure(ctrl) {
    const bar = ctrl.bar;
    ctrl.buttons = buttonsOf(bar);
    ctrl.boxes = ctrl.buttons.map(b => ({ x: b.offsetLeft, y: b.offsetTop, w: b.offsetWidth, h: b.offsetHeight }));
    ctrl.w = bar.offsetWidth;
    ctrl.h = bar.offsetHeight;
    ctrl.s = Math.max(0.5, ctrl.h / 64); // dp scale relative to the 64dp iOS tab bar
    const thumbH = ctrl.boxes.length ? Math.max(...ctrl.boxes.map(b => b.h)) : ctrl.h;
    const padX = ctrl.boxes.length ? Math.max(0, ctrl.boxes[0].x) : 0;
    ctrl.plateHalf = [Math.max(1, ctrl.w / 2 - padX), thumbH / 2];
    ctrl.margin = Math.ceil(thumbH * (PRESSED_SCALE * 1.25 - 1) / 2 + 12);
    ctrl.colors = palette();
    ctrl.backdrop = resolveBackdrop(bar, ctrl.colors.base);
    for (const cv of [ctrl.canvas, ctrl.lens]) {
      cv.style.setProperty('top', -ctrl.margin + 'px', 'important');
      cv.style.setProperty('left', -ctrl.margin + 'px', 'important');
    }
    // Label geometry is read from the DOM only while nothing is scaled.
    if (!ctrl.labels || ctrl.press < 0.001) ctrl.labels = layoutLabels(ctrl);
  }

  /* ---------------- labels seen through the lens ---------------- */
  const symbolCache = new Map();
  function symbolPaths(svg) {
    const use = svg.querySelector('use');
    const id = use ? (use.getAttribute('href') || use.getAttribute('xlink:href') || '').replace(/^#/, '') : '';
    const source = (id && document.getElementById(id)) || svg;
    const key = id || source;
    if (symbolCache.has(key)) return symbolCache.get(key);
    const vb = (source.getAttribute('viewBox') || '0 0 24 24').trim().split(/[\s,]+/).map(Number);
    const paths = [];
    for (const n of source.querySelectorAll('path')) {
      const d = n.getAttribute('d');
      if (d) paths.push(new Path2D(d));
    }
    for (const n of source.querySelectorAll('circle')) {
      const c = new Path2D();
      c.arc(+n.getAttribute('cx') || 0, +n.getAttribute('cy') || 0, +n.getAttribute('r') || 0, 0, Math.PI * 2);
      paths.push(c);
    }
    const data = { x: vb[0] || 0, y: vb[1] || 0, w: vb[2] || 24, h: vb[3] || 24, paths };
    symbolCache.set(key, data);
    return data;
  }

  // Positions are stored relative to each segment's centre so the raster can
  // apply the same lerp(1, 1.2) content scale the DOM labels get while pressed.
  let measureCtx = null;
  function layoutLabels(ctrl) {
    const barRect = ctrl.bar.getBoundingClientRect();
    const k = barRect.width / Math.max(1, ctrl.bar.offsetWidth) || 1;
    measureCtx = measureCtx || document.createElement('canvas').getContext('2d');
    return ctrl.buttons.map((btn, i) => {
      const box = ctrl.boxes[i];
      const cx = box.x + box.w / 2, cy = box.y + box.h / 2;
      const cs = getComputedStyle(btn);
      const local = r => ({ x: (r.left - barRect.left) / k - cx, y: (r.top - barRect.top) / k - cy, w: r.width / k, h: r.height / k });
      // Emphasis is spatial, as in the reference: every label is drawn here in
      // the accent colour, so whatever the indicator covers is tinted and the
      // DOM labels outside it keep the normal content colour.
      const item = { cx, cy, font: `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}` };
      const svg = btn.querySelector('svg');
      if (svg) item.icon = { box: local(svg.getBoundingClientRect()), sym: symbolPaths(svg) };
      const walker = document.createTreeWalker(btn, NodeFilter.SHOW_TEXT, {
        acceptNode: n => (n.textContent.trim() && !n.parentElement.closest('svg')) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT
      });
      const nodes = [];
      for (let n = walker.nextNode(); n; n = walker.nextNode()) nodes.push(n);
      if (nodes.length) {
        const range = document.createRange();
        range.setStart(nodes[0], 0);
        range.setEnd(nodes[nodes.length - 1], nodes[nodes.length - 1].length);
        const tbox = local(range.getBoundingClientRect());
        measureCtx.font = item.font;
        const text = nodes.map(n => n.textContent).join('').replace(/\s+/g, ' ').trim();
        const m = measureCtx.measureText(text);
        const ascent = m.fontBoundingBoxAscent || parseFloat(cs.fontSize) * 0.8;
        // Centre on the DOM text so the wider bold glyphs grow evenly both ways.
        item.text = { value: text, center: tbox.x + tbox.w / 2, baseline: tbox.y + ascent };
      }
      return item;
    });
  }

  function drawLabels(ctrl, W, H, dpr, margin, contentScale) {
    const raster = ctrl.labelCanvas || (ctrl.labelCanvas = document.createElement('canvas'));
    if (raster.width !== W || raster.height !== H) { raster.width = W; raster.height = H; }
    const ctx = raster.getContext('2d');
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, W, H);
    for (const item of ctrl.labels || []) {
      ctx.setTransform(dpr * contentScale, 0, 0, dpr * contentScale, (margin + item.cx) * dpr, (margin + item.cy) * dpr);
      ctx.fillStyle = ctrl.colors.accent;
      if (item.icon && item.icon.sym.paths.length) {
        const { box, sym } = item.icon;
        ctx.save();
        ctx.translate(box.x, box.y);
        ctx.scale(box.w / sym.w, box.h / sym.h);
        ctx.translate(-sym.x, -sym.y);
        for (const path of sym.paths) ctx.fill(path);
        ctx.restore();
      }
      if (item.text) {
        ctx.font = item.font;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'alphabetic';
        ctx.fillText(item.text.value, item.text.center, item.text.baseline);
      }
    }
    return raster;
  }

  // Thumb geometry at a fractional index, interpolating neighbouring segments.
  function thumbAt(ctrl, value) {
    const boxes = ctrl.boxes;
    if (!boxes.length) return null;
    const v = Math.max(0, Math.min(boxes.length - 1, value));
    const i = Math.min(boxes.length - 2, Math.floor(v));
    if (i < 0) return boxes[0];
    const a = boxes[i], b = boxes[i + 1], t = v - i;
    return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t, w: a.w + (b.w - a.w) * t, h: a.h + (b.h - a.h) * t };
  }

  // Fractional index under a bar-local x (inverse of thumbAt by centres).
  function valueAtX(ctrl, x) {
    const c = ctrl.boxes.map(b => b.x + b.w / 2);
    if (!c.length) return 0;
    if (x <= c[0]) return 0;
    for (let i = 0; i < c.length - 1; i++) {
      if (x <= c[i + 1]) return i + (x - c[i]) / Math.max(1, c[i + 1] - c[i]);
    }
    return c.length - 1;
  }

  function draw(ctrl) {
    const bar = ctrl.bar;
    if (!bar.isConnected || !ctrl.boxes || !ctrl.boxes.length) return;
    const { w, h, margin, s } = ctrl;
    const p = ctrl.press;
    bar.style.setProperty('--lt-content-scale', String(1 + 0.2 * p));
    bar.style.transform = p > 0.001 ? `scale(${1 + (16 * s / Math.max(1, w)) * p})` : '';

    const dpr = Math.min(window.devicePixelRatio || 1, 2.5, (OFFSCREEN_W - 2) / (w + margin * 2));
    const totalW = w + margin * 2, totalH = h + margin * 2;
    const W = Math.round(totalW * dpr), H = Math.min(OFFSCREEN_H, Math.round(totalH * dpr));
    for (const canvas of [ctrl.canvas, ctrl.lens]) {
      if (canvas.width !== W || canvas.height !== H) {
        canvas.width = W;
        canvas.height = H;
        canvas.style.width = totalW + 'px';
        canvas.style.height = totalH + 'px';
      }
    }
    const thumb = thumbAt(ctrl, ctrl.value);
    const vel = ctrl.velocity / 10;
    const velX = Math.max(-0.2, Math.min(0.2, vel * 0.75));
    const velY = Math.max(-0.2, Math.min(0.2, vel * 0.25));
    const scaleX = ctrl.scaleX / (1 - velX);
    const scaleY = ctrl.scaleY * (1 - velY);
    const c = ctrl.colors;
    const ctx2d = ctrl.ctx;
    const sGL = initGL();
    if (!sGL) {
      ctx2d.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx2d.clearRect(0, 0, totalW, totalH);
      ctx2d.fillStyle = `rgba(${c.track.slice(0, 3).map(v => Math.round(v * 255))},${c.track[3]})`;
      ctx2d.beginPath(); ctx2d.roundRect(margin, margin, w, h, h / 2); ctx2d.fill();
      ctx2d.fillStyle = `rgba(${c.dim.slice(0, 3).map(v => Math.round(v * 255))},${c.dim[3]})`;
      ctx2d.beginPath(); ctx2d.roundRect(margin + thumb.x, margin + thumb.y, thumb.w, thumb.h, thumb.h / 2); ctx2d.fill();
      ctrl.lensCtx.clearRect(0, 0, W, H);
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
    gl.uniform1f(u.uTop, OFFSCREEN_H);
    gl.uniform2f(u.uTrackC, (margin + w / 2) * k, (margin + h / 2) * k);
    gl.uniform2f(u.uTrackHalf, w / 2 * k, h / 2 * k);
    gl.uniform4f(u.uTrackColor, c.track[0], c.track[1], c.track[2], c.track[3]);
    gl.uniform3f(u.uBackdrop, ctrl.backdrop[0], ctrl.backdrop[1], ctrl.backdrop[2]);
    gl.uniform4f(u.uDim, c.dim[0], c.dim[1], c.dim[2], c.dim[3]);
    gl.uniform2f(u.uPlateHalf, ctrl.plateHalf[0] * k, ctrl.plateHalf[1] * k);
    gl.uniform2f(u.uThumbC, (margin + thumb.x + thumb.w / 2) * k, (margin + thumb.y + thumb.h / 2) * k);
    gl.uniform2f(u.uThumbHalf, thumb.w / 2 * k, thumb.h / 2 * k);
    gl.uniform2f(u.uLayerScale, scaleX, scaleY);
    gl.uniform1f(u.uPress, p);
    // Indicator lens(10dp, 14dp) with chromatic aberration.
    gl.uniform1f(u.uRefrH, 10 * LENS_GAIN * s * p * k);
    gl.uniform1f(u.uRefrA, -14 * LENS_GAIN * s * p * k);
    // Shadow.Default (24dp, 10%) with alpha = press: no shadow at rest.
    gl.uniform1f(u.uShadowR, 24 * s * k);
    gl.uniform1f(u.uShadowA, c.shadow * p);
    gl.uniform2f(u.uShadowOff, 0, 4 * s * k);
    gl.uniform1f(u.uInnerR, Math.max(0.001, 8 * s * p * k));
    gl.uniform1f(u.uInnerA, 0.3 * p);
    gl.uniform2f(u.uInnerOff, 0, 8 * s * p * k);
    gl.uniform1f(u.uHlWidth, Math.max(1.5, 1 * s * k));
    gl.uniform1f(u.uHlAlpha, p);
    // Plate shadow/rim: Shadow.Default is black 10% x 0.5 with a 24dp sigma
    // over a colourful wallpaper. On a plain card that is invisible, so the
    // shadow is tighter and darker here; the rim is a 1dp white stroke.
    gl.uniform1f(u.uPlateShadowSigma, Math.max(1, 6 * s * k));
    gl.uniform1f(u.uPlateShadowOff, 4 * s * k);
    gl.uniform1f(u.uPlateShadowA, isDark() ? 0.35 : 0.28);
    gl.uniform1f(u.uPlateHlW, Math.max(1.5, 2 * s * k));
    gl.uniform2f(u.uCanvas, W, H);
    // Pass 1 (below the DOM labels): track + thumb shadow.
    gl.uniform1f(u.uMode, 0);
    gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
    ctx2d.setTransform(1, 0, 0, 1, 0, 0);
    ctx2d.clearRect(0, 0, W, H);
    ctx2d.drawImage(sGL.canvas, 0, 0, W, H, 0, 0, W, H);
    // Pass 2 (above the DOM labels): the thumb, refracting a raster of the labels.
    const raster = drawLabels(ctrl, W, H, dpr, margin, 1 + 0.2 * p);
    gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D, sGL.labelTex);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, raster);
    gl.uniform1i(u.uLabels, 0);
    gl.uniform1f(u.uMode, 1);
    gl.clear(gl.COLOR_BUFFER_BIT);
    gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
    const lctx = ctrl.lensCtx;
    lctx.setTransform(1, 0, 0, 1, 0, 0);
    lctx.clearRect(0, 0, W, H);
    lctx.drawImage(sGL.canvas, 0, 0, W, H, 0, 0, W, H);
  }

  function press(ctrl) {
    if (reducedMotion.matches) return;
    ctrl.targetPress = 1;
    ctrl.targetScaleX = PRESSED_SCALE;
    ctrl.targetScaleY = PRESSED_SCALE;
  }

  // animateToValue: press + animate + release (for taps, keys and app-driven changes).
  function setTarget(ctrl, index, animate = true) {
    if (ctrl.dragging || ctrl.target === index) return;
    ctrl.target = index;
    if (!animate) {
      ctrl.value = index;
      ctrl.valueV = 0;
      ctrl.needsDraw = true;
      wake();
      return;
    }
    ctrl.trackVelocity = false;
    ctrl.velocity = ctrl.velocityV = ctrl.targetVelocity = 0;
    ctrl.samples.length = 0;
    if (ctrl.targetPress === 0) press(ctrl);
    wake();
  }

  function step(ctrl, dt, now) {
    let active = false;
    // Never auto-release while the thumb is still held (press-and-drag).
    if (ctrl.targetPress === 1 && !ctrl.held && !ctrl.dragging && Math.abs(ctrl.target - ctrl.value) < 0.02) {
      ctrl.targetPress = 0;
      ctrl.targetScaleX = 1;
      ctrl.targetScaleY = 1;
    }
    if (Math.abs(ctrl.target - ctrl.value) > THRESHOLD || Math.abs(ctrl.valueV) > THRESHOLD) {
      [ctrl.value, ctrl.valueV] = springCritical(ctrl.value, ctrl.valueV, ctrl.target, dt, VALUE_OMEGA);
      if (ctrl.trackVelocity || ctrl.dragging) {
        ctrl.samples.push({ t: now, p: ctrl.value });
        if (ctrl.samples.length > 20) ctrl.samples.shift();
        ctrl.targetVelocity = trackerVelocity(ctrl.samples) / Math.max(1, ctrl.boxes.length - 1);
      }
      active = true;
    } else {
      ctrl.value = ctrl.target;
      ctrl.valueV = 0;
      if (!ctrl.dragging) {
        ctrl.targetVelocity = 0;
        ctrl.trackVelocity = false;
        ctrl.samples.length = 0;
      }
    }
    for (const [key, spec] of [['press', null], ['scaleX', SCALE_X], ['scaleY', SCALE_Y], ['velocity', VELOCITY]]) {
      const tKey = 'target' + key[0].toUpperCase() + key.slice(1), vKey = key + 'V';
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
    let any = false;
    for (const ctrl of controllers.values()) {
      if (!ctrl.bar.isConnected) { ctrl.dispose(); continue; }
      const active = step(ctrl, dt, now);
      if (active || ctrl.needsDraw) {
        draw(ctrl);
        ctrl.needsDraw = false;
      }
      if (active) any = true;
    }
    if (any) raf = requestAnimationFrame(loop);
    else prevT = 0;
  }

  function sync(ctrl, animate = true) {
    const hadButtons = !!(ctrl.buttons && ctrl.buttons.length);
    measure(ctrl);
    const index = activeIndex(ctrl);
    ctrl.buttons.forEach((b, i) => b.classList.toggle('lt-selected', i === index));
    // A bar whose buttons are filled in later by script starts where it is,
    // instead of sliding over from the first tab.
    if (ctrl.target === undefined || !hadButtons) {
      ctrl.target = ctrl.value = index;
      ctrl.needsDraw = true;
      wake();
      return;
    }
    // Hidden bars (inactive panes) jump instead of animating.
    setTarget(ctrl, index, animate && ctrl.bar.offsetParent !== null);
    ctrl.needsDraw = true;
    wake();
  }

  function setupBar(bar) {
    if (controllers.has(bar)) return;
    bar.classList.add('has-liquid-tabbar');
    bar.querySelectorAll(':scope > .liquid-tabbar-canvas, :scope > .liquid-tabbar-lens').forEach(c => c.remove());
    const canvas = document.createElement('canvas');
    canvas.className = 'liquid-tabbar-canvas';
    canvas.setAttribute('aria-hidden', 'true');
    bar.prepend(canvas);
    const lens = document.createElement('canvas');
    lens.className = 'liquid-tabbar-lens';
    lens.setAttribute('aria-hidden', 'true');
    bar.append(lens);
    const abort = new AbortController();
    const ctrl = {
      bar, canvas, ctx: canvas.getContext('2d'), lens, lensCtx: lens.getContext('2d'), samples: [], dragging: false, needsDraw: true,
      target: undefined, value: 0, valueV: 0,
      press: 0, pressV: 0, targetPress: 0,
      scaleX: 1, scaleXV: 0, targetScaleX: 1,
      scaleY: 1, scaleYV: 0, targetScaleY: 1,
      velocity: 0, velocityV: 0, targetVelocity: 0,
      dispose() {
        abort.abort();
        ro.disconnect();
        mo.disconnect();
        controllers.delete(bar);
      }
    };
    controllers.set(bar, ctrl);
    const listen = (el, type, fn, opts = {}) => el.addEventListener(type, fn, { ...opts, signal: abort.signal });

    // Re-measure only: the thumb is stored as a fractional index, so it keeps
    // animating across the new segment boxes instead of jumping to its target.
    const ro = new ResizeObserver(() => { measure(ctrl); ctrl.needsDraw = true; wake(); });
    ro.observe(bar);
    const mo = new MutationObserver(records => {
      // lt-selected toggles only mutate when the selection really changed, so this settles.
      if (records.some(r => r.target !== canvas && r.target !== lens)) { ctrl.labels = null; sync(ctrl); }
    });
    mo.observe(bar, { subtree: true, childList: true, attributes: true, attributeFilter: ['class', 'aria-selected', 'hidden'] });

    // Pointer: pressing the selected thumb grabs it (drag to move);
    // pressing another segment just lets the native click select it.
    let pointer = null, suppressClickUntil = 0;
    listen(bar, 'pointerdown', e => {
      if (e.button !== 0 || !e.isPrimary || pointer) return;
      const b = e.target.closest('button');
      measure(ctrl);
      const i = ctrl.buttons.indexOf(b);
      if (i < 0) return;
      pointer = { id: e.pointerId, x: e.clientX, y: e.clientY, grabbed: i === Math.round(ctrl.target ?? i), dragged: false, start: ctrl.target ?? i };
      if (pointer.grabbed) { ctrl.held = true; press(ctrl); wake(); }
    });
    listen(window, 'pointermove', e => {
      if (!pointer || e.pointerId !== pointer.id || !pointer.grabbed) return;
      const dx = e.clientX - pointer.x, dy = e.clientY - pointer.y;
      if (!pointer.dragged) {
        if (Math.abs(dy) > 8 && Math.abs(dy) > Math.abs(dx)) { pointer = null; ctrl.held = false; wake(); return; }
        if (Math.abs(dx) < 4) return;
        pointer.dragged = true;
        ctrl.dragging = true;
        press(ctrl);
        ctrl.samples.length = 0;
        try { bar.setPointerCapture(e.pointerId); } catch (_) {}
      }
      if (e.cancelable) e.preventDefault();
      const rect = bar.getBoundingClientRect();
      const startBox = thumbAt(ctrl, pointer.start);
      const x = startBox.x + startBox.w / 2 + dx * (bar.offsetWidth / Math.max(1, rect.width));
      ctrl.target = Math.max(0, Math.min(ctrl.boxes.length - 1, valueAtX(ctrl, x)));
      wake();
    }, { passive: false });
    const release = (e, cancel) => {
      if (!pointer || (e && e.pointerId !== pointer.id)) return;
      const p = pointer;
      pointer = null;
      ctrl.held = false;
      try { bar.releasePointerCapture(p.id); } catch (_) {}
      if (!p.dragged) { wake(); return; }
      suppressClickUntil = performance.now() + 400;
      ctrl.dragging = false;
      ctrl.trackVelocity = true;
      const index = cancel ? Math.round(p.start) : Math.round(ctrl.target);
      ctrl.target = index;
      const b = ctrl.buttons[index];
      if (!cancel && b && !b.disabled && !isActive(b)) b.click();
      else ctrl.target = Math.round(p.start);
      wake();
    };
    listen(window, 'pointerup', e => release(e, false));
    listen(window, 'pointercancel', e => release(e, true));
    listen(bar, 'click', e => {
      if (e.isTrusted && performance.now() < suppressClickUntil && e.detail !== 0) { e.preventDefault(); e.stopImmediatePropagation(); }
    }, { capture: true });
    listen(bar, 'click', () => requestAnimationFrame(() => sync(ctrl)));
    listen(bar, 'keydown', e => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) return;
      const list = buttonsOf(bar).filter(b => !b.disabled), i = list.indexOf(e.target);
      if (i < 0) return;
      e.preventDefault();
      const rtl = getComputedStyle(bar).direction === 'rtl';
      const dir = (e.key === 'ArrowRight') !== rtl ? 1 : -1;
      const next = e.key === 'Home' ? 0 : e.key === 'End' ? list.length - 1 : (i + dir + list.length) % list.length;
      list[next].focus({ preventScroll: true });
      list[next].click();
    });
    sync(ctrl, false);
  }

  function scan() { document.querySelectorAll(BAR_SELECTOR).forEach(setupBar); }

  function init() {
    scan();
    new MutationObserver(records => {
      let rescan = false, theme = false;
      for (const r of records) {
        if (r.type === 'childList') rescan = true;
        else if (r.target === document.body || r.target === document.documentElement) theme = true;
      }
      if (rescan) scan();
      if (theme) controllers.forEach(ctrl => { measure(ctrl); ctrl.needsDraw = true; });
      if (rescan || theme) wake();
    }).observe(document.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'data-theme'] });
    window.addEventListener('resize', () => { controllers.forEach(ctrl => { measure(ctrl); ctrl.needsDraw = true; }); wake(); }, { passive: true });
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init, { once: true });
  else init();

  window.MUILiquidTabBar = {
    init,
    scan,
    controllers,
    sync: bar => { const c = controllers.get(bar); if (c) sync(c); }
  };
})();
