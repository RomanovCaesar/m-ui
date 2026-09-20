/* m-ui WebGL liquid glass renderer.
 *
 * A framework-free port of the glass MATERIAL from the liquid-glass-webgl
 * reference project (github.com/martin65536/liquid-glass-webgl). A single
 * full-viewport canvas OWNS the wallpaper (drawn into the canvas, no DOM
 * <img> behind it) so the glass shader samples the same texels shown behind
 * each surface — exactly the reference's model. Chrome surfaces (sidebar,
 * toolbar, cards-as-chrome, modals, menus, buttons, toggles, tab lenses) are
 * registered as rounded rects; the shader refracts, blurs and lights the
 * wallpaper at each rect. Text and controls stay as real DOM on top.
 *
 * Ported math (faithful to the reference GLSL):
 *   - sdRoundedRect / gradSdRoundedRect        (shaders/sdf.ts)
 *   - circleMap(x) = 1 - sqrt(1 - x*x)          (shaders/element-utils.ts)
 *   - lens refraction: offset = d * grad         (shaders/element.ts)
 *   - applyColorControls (vibrancy)              (shaders/element-utils.ts)
 *   - Vogel golden-angle Gaussian blur disc      (shaders/element-utils.ts)
 *   - directional rim specular stroke            (shaders/highlight.ts)
 *
 * No source or assets from those repositories are vendored; this is an
 * independent implementation of the same material. WebGL1, no extensions.
 */
(() => {
  'use strict';
  if (window.MUILiquidGlassGL) return;

  const reducedTransparency = matchMedia('(prefers-reduced-transparency: reduce)');
  const highContrast = matchMedia('(prefers-contrast: more), (forced-colors: active)');

  const SURFACE_SELECTOR = '.lg-surface';
  // Content-overlay surfaces float ABOVE opaque page content, so the back-layer
  // canvas is occluded behind them and cannot paint their glass. They keep a
  // strong frosted CSS material instead (see liquid-glass-gl.css) and are
  // skipped here so we don't waste passes or bleed a rim outside the panel.
  // The tab-selection pill lives INSIDE opaque cards, so it is occluded from
  // the back-layer canvas the same way overlays are. It gets a CSS frosted
  // material that refracts the label text beneath it (the segmented-control
  // look) while liquid-optics.js keeps driving its spring position/squash.
  const SKIP_SELECTOR = '.modal-panel,.drawer,.swift-picker-menu,.general-menu,.auto-refresh-menu,.settings-menu,.liquid-pill-slider';
  const WALLPAPER_URL = 'static/media/glass-wallpaper.svg';

  // Per-surface material tuning. Values are in CSS px unless noted; the
  // renderer multiplies lengths by devicePixelRatio at draw time.
  const PARAMS = {
    // Refraction band: how far in from the rim the lens bends the backdrop.
    refractBand: 18,      // px, clamped to min(halfW, halfH)
    refractAmount: 26,    // px, peak displacement at the rim
    blur: 7,              // px, Gaussian backdrop blur radius
    // Vibrancy (color matrix). brightness in [-1,1], contrast/saturation ~1.
    brightness: 0.02,
    contrast: 1.0,
    saturation: 1.28,
    // Bevel lighting strength (edge light/shadow following uLightAngle).
    bevel: 0.9,
    lightAngleDeg: -68,   // top-ish light
    // Rim specular (directional edge highlight, follows pointer).
    rimStroke: 2.4,       // px, stroke band width
    rimBlur: 1.1,         // px, stroke softening
    rimFalloff: 2.2,
    rimAlpha: 0.9,
    // Glass body tint (translucent). Light theme = cool white veil.
    tintLight: [0.96, 0.98, 1.0, 0.14],
    tintDark: [0.10, 0.14, 0.22, 0.30],
    // Wallpaper brightness per theme (canvas owns the wallpaper).
    dimLight: 1.0,
    dimDark: 0.36,
    dimUltra: 0.13,
  };

  // Per-surface overrides keyed by matches() selector, applied in order.
  const OVERRIDES = [
    { sel: '.sidebar', blur: 11, tintLightA: 0.18, refractBand: 26 },
    { sel: '.topbar', blur: 6, refractBand: 16 },
    { sel: '.login-card', blur: 9, tintLightA: 0.16, refractBand: 30, refractAmount: 34 },
    { sel: '.modal-panel,.drawer,.swift-picker-menu,.general-menu,.auto-refresh-menu,.settings-menu', blur: 14, tintLightA: 0.24, refractBand: 22 },
    { sel: '.liquid-pill-slider', blur: 1.2, tintLightA: 0.10, tintDarkA: 0.14, refractBand: 10, refractAmount: 16, rimAlpha: 1.0 },
    { sel: '.outline-btn,.icon-btn,.inbound-pill,.small-btn,.sider-trigger,.settings-button,.primary-btn', blur: 2, refractBand: 9, refractAmount: 13 },
  ];

  // ---- Gaussian blur disc (Vogel spiral), baked into the shader ----------
  function blurTaps(n) {
    if (n <= 1) return [{ x: 0, y: 0, w: 1 }];
    const golden = Math.PI * (3 - Math.sqrt(5));
    const maxR = 3, taps = []; let tot = 0;
    for (let i = 0; i < n; i++) {
      const t = (i + 0.5) / n, r = maxR * Math.sqrt(t), a = i * golden;
      const x = r * Math.cos(a), y = r * Math.sin(a), w = Math.exp(-0.5 * (x * x + y * y));
      taps.push({ x, y, w }); tot += w;
    }
    for (const tp of taps) tp.w /= tot;
    return taps;
  }
  const TAPS = blurTaps(16);
  const BLUR_GLSL = TAPS.map(t =>
    `    sum += texture2D(uWall, uv + vec2(${t.x.toFixed(5)}, ${t.y.toFixed(5)}) * pxToUv) * ${t.w.toFixed(7)};`
  ).join('\n');

  const VERT = `
    attribute vec2 aUnit;
    uniform vec4 uRectClip;   // x0,y0 (top-left) .. x1,y1 (bottom-right) in clip space
    void main() {
      vec2 p = mix(uRectClip.xy, uRectClip.zw, aUnit);
      gl_Position = vec4(p, 0.0, 1.0);
    }`;

  // Shared SDF/backdrop helpers (faithful to sdf.ts + element-utils.ts).
  const COMMON = `
    precision highp float;
    uniform sampler2D uWall;
    uniform vec2 uCanvas;       // device px
    uniform vec2 uWallSize;     // texture px
    float sdRoundedRect(vec2 c, vec2 h, float r) {
      vec2 q = abs(c) - (h - vec2(r));
      return length(max(q, 0.0)) - r + min(max(q.x, q.y), 0.0);
    }
    vec2 gradSdRoundedRect(vec2 c, vec2 h, float r) {
      vec2 q = abs(c) - (h - vec2(r));
      if (q.x >= 0.0 || q.y >= 0.0) {
        vec2 v = max(q, vec2(0.0)); float l = length(v);
        if (l < 1e-6) return vec2(0.0);
        return sign(c) * (v / l);
      }
      float gx = step(q.y, q.x);
      return sign(c) * vec2(gx, 1.0 - gx);
    }
    float circleMap(float x) { return 1.0 - sqrt(max(0.0, 1.0 - x * x)); }
    vec2 coverUv(vec2 px) {
      float ca = uCanvas.x / uCanvas.y, wa = uWallSize.x / uWallSize.y;
      vec2 uv = px / uCanvas;
      if (wa > ca) uv.x = (uv.x - 0.5) * (ca / wa) + 0.5;
      else uv.y = (uv.y - 0.5) * (wa / ca) + 0.5;
      return uv;
    }
    vec2 pxToUvScale() {
      float ca = uCanvas.x / uCanvas.y, wa = uWallSize.x / uWallSize.y;
      if (wa > ca) return vec2(ca / wa, 1.0) / uCanvas;
      return vec2(1.0, wa / ca) / uCanvas;
    }`;

  const WALL_FRAG = COMMON + `
    uniform float uDim;
    void main() {
      vec2 px = vec2(gl_FragCoord.x, uCanvas.y - gl_FragCoord.y);
      vec2 uv = coverUv(px);
      vec3 c = texture2D(uWall, uv).rgb * uDim;
      gl_FragColor = vec4(c, 1.0);
    }`;

  const GLASS_FRAG = COMMON + `
    uniform vec2 uOffset;      // element top-left, device px
    uniform vec2 uSize;        // device px
    uniform float uRadius;     // device px
    uniform float uBand;       // refraction band, device px
    uniform float uAmount;     // refraction peak displacement, device px
    uniform float uBlur;       // device px
    uniform float uBright, uContrast, uSat;
    uniform vec4 uTint;
    uniform float uBevel, uLightAngle;
    vec3 colorControls(vec3 c) {
      float invS = 1.0 - uSat;
      float r = 0.213 * invS, g = 0.715 * invS, b = 0.072 * invS;
      float t = 0.5 - uContrast * 0.5 + uBright;
      float cs = uContrast * uSat, cr = uContrast * r, cg = uContrast * g, cb = uContrast * b;
      return vec3(
        (cr + cs) * c.r + cg * c.g + cb * c.b + t,
        cr * c.r + (cg + cs) * c.g + cb * c.b + t,
        cr * c.r + cg * c.g + (cb + cs) * c.b + t);
    }
    vec4 sampleBlurred(vec2 px, float radius) {
      vec2 uv = coverUv(px);
      if (radius < 0.5) return texture2D(uWall, uv);
      vec2 pxToUv = radius * pxToUvScale();
      vec4 sum = vec4(0.0);
${BLUR_GLSL}
      return sum;
    }
    void main() {
      vec2 screen = vec2(gl_FragCoord.x, uCanvas.y - gl_FragCoord.y);
      vec2 center = uOffset + uSize * 0.5;
      vec2 centered = screen - center;
      vec2 halfSize = uSize * 0.5;
      float sd = sdRoundedRect(centered, halfSize, uRadius);
      if (sd > 1.0) discard;
      float edge = 1.0 - smoothstep(-1.0, 1.0, sd);

      float gradR = min(uRadius * 1.5, min(halfSize.x, halfSize.y));
      vec2 grad = gradSdRoundedRect(centered, halfSize, gradR);

      vec2 sampleCoord = screen;
      float intensity = 0.0;
      if (uBand > 0.5 && (-sd) < uBand) {
        float sdC = min(sd, 0.0);
        float k = 1.0 - (-sdC) / uBand;      // 0 deep .. 1 at rim
        intensity = k;
        float d = circleMap(k) * uAmount;
        sampleCoord = screen + d * grad;
      }
      vec4 bg = sampleBlurred(sampleCoord, uBlur);
      vec3 color = colorControls(bg.rgb);

      // Bevel: brighten toward light on the rim, darken on the far side.
      if (uBand > 0.5) {
        vec2 lightDir = vec2(cos(uLightAngle), sin(uLightAngle));
        float b1 = clamp(dot(grad, lightDir), 0.0, 1.0);
        color *= 1.0 + 0.5 * intensity * b1 * uBevel;
        float b2 = clamp(dot(grad, -lightDir), 0.0, 1.0);
        float band = smoothstep(1.0, 0.0, abs(intensity - 0.25) * 6.0);
        color *= 1.0 - 0.22 * b2 * band * uBevel;
      }

      // Translucent glass body tint.
      color = mix(color, uTint.rgb, uTint.a);

      float coverage = edge;
      gl_FragColor = vec4(color * coverage, coverage);
    }`;

  const RIM_FRAG = COMMON + `
    uniform vec2 uOffset;
    uniform vec2 uSize;
    uniform float uRadius;
    uniform vec3 uRimColor;
    uniform float uRimAngle, uRimFalloff, uRimAlpha, uRimStroke, uRimBlur;
    void main() {
      vec2 screen = vec2(gl_FragCoord.x, uCanvas.y - gl_FragCoord.y);
      vec2 center = uOffset + uSize * 0.5;
      vec2 centered = screen - center;
      vec2 halfSize = uSize * 0.5;
      float sd = sdRoundedRect(centered, halfSize, uRadius);
      if (sd > 0.0) discard;
      float strokeHalf = uRimStroke * 0.5;
      float sigma = max(uRimBlur, 0.1);
      float mask = 0.0, wSum = 0.0;
      for (int i = -1; i <= 1; i++) {
        float off = float(i) * sigma;
        float hard = (abs(sd - off) < strokeHalf) ? 1.0 : 0.0;
        float w = exp(-0.5 * (off * off) / (sigma * sigma));
        mask += hard * w; wSum += w;
      }
      mask = mask / wSum * 0.5;
      float gradR = min(uRadius * 1.5, min(halfSize.x, halfSize.y));
      vec2 grad = gradSdRoundedRect(centered, halfSize, gradR);
      vec2 normal = vec2(cos(uRimAngle), sin(uRimAngle));
      float d = dot(grad, normal);
      float ints = pow(abs(d), uRimFalloff);
      vec3 c = uRimColor * ints * mask * uRimAlpha;
      gl_FragColor = vec4(c, 1.0);
    }`;

  // ---- WebGL plumbing ----------------------------------------------------
  let gl, canvas, wallProg, glassProg, rimProg, quad, wallTex, wallW = 1, wallH = 1;
  let active = false, rafId = 0, sig = '', idle = 0, pointerAngle = -0.9;
  const surfaces = new Map();

  function compile(type, src) {
    const s = gl.createShader(type);
    gl.shaderSource(s, src); gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) {
      console.warn('[liquid-glass-gl] shader error:', gl.getShaderInfoLog(s));
      return null;
    }
    return s;
  }
  function program(frag) {
    const v = compile(gl.VERTEX_SHADER, VERT), f = compile(gl.FRAGMENT_SHADER, frag);
    if (!v || !f) return null;
    const p = gl.createProgram();
    gl.attachShader(p, v); gl.attachShader(p, f); gl.linkProgram(p);
    if (!gl.getProgramParameter(p, gl.LINK_STATUS)) {
      console.warn('[liquid-glass-gl] link error:', gl.getProgramInfoLog(p));
      return null;
    }
    p._u = {}; p._a = { aUnit: gl.getAttribLocation(p, 'aUnit') };
    const n = gl.getProgramParameter(p, gl.ACTIVE_UNIFORMS);
    for (let i = 0; i < n; i++) { const u = gl.getActiveUniform(p, i); p._u[u.name] = gl.getUniformLocation(p, u.name); }
    return p;
  }
  const U = (p, name) => p._u[name];

  function themeDim() {
    const root = document.documentElement;
    if (root.dataset.theme === 'ultra-dark' || root.classList.contains('ultra-dark')) return PARAMS.dimUltra;
    if (document.body.classList.contains('dark') || root.classList.contains('dark')) return PARAMS.dimDark;
    return PARAMS.dimLight;
  }
  const isDark = () => document.body.classList.contains('dark') || document.documentElement.classList.contains('dark');

  function paramsFor(el) {
    const p = { ...PARAMS };
    for (const o of OVERRIDES) {
      if (!el.matches(o.sel)) continue;
      if (o.blur != null) p.blur = o.blur;
      if (o.refractBand != null) p.refractBand = o.refractBand;
      if (o.refractAmount != null) p.refractAmount = o.refractAmount;
      if (o.rimAlpha != null) p.rimAlpha = o.rimAlpha;
      if (o.tintLightA != null) p._tintLightA = o.tintLightA;
      if (o.tintDarkA != null) p._tintDarkA = o.tintDarkA;
    }
    return p;
  }

  function scan() {
    const found = new Set();
    document.querySelectorAll(SURFACE_SELECTOR).forEach(el => {
      if (el.closest(SKIP_SELECTOR)) return; // overlay panels use CSS material
      found.add(el);
      if (!surfaces.has(el)) surfaces.set(el, { el, p: paramsFor(el) });
    });
    for (const el of surfaces.keys()) if (!found.has(el) || !el.isConnected) surfaces.delete(el);
  }

  function rectOf(el) {
    const r = el.getBoundingClientRect();
    if (r.width < 2 || r.height < 2) return null;
    const cs = getComputedStyle(el);
    if (cs.visibility !== 'visible' || cs.opacity === '0' || cs.display === 'none') return null;
    if (r.bottom < 0 || r.top > innerHeight || r.right < 0 || r.left > innerWidth) return null;
    const radius = Math.min(parseFloat(cs.borderTopLeftRadius) || 16, r.width / 2, r.height / 2);
    return { x: r.left, y: r.top, w: r.width, h: r.height, radius };
  }

  function signature() {
    let s = `${innerWidth}x${innerHeight}:${themeDim()}:${pointerAngle.toFixed(2)}:`;
    for (const st of surfaces.values()) {
      const r = st.el.getBoundingClientRect();
      s += `${Math.round(r.left)},${Math.round(r.top)},${Math.round(r.width)},${Math.round(r.height)};`;
    }
    return s;
  }

  function resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const w = Math.round(innerWidth * dpr), h = Math.round(innerHeight * dpr);
    if (canvas.width !== w || canvas.height !== h) {
      canvas.width = w; canvas.height = h;
      canvas.style.width = innerWidth + 'px'; canvas.style.height = innerHeight + 'px';
    }
    return dpr;
  }

  function draw() {
    const dpr = resize();
    const CW = canvas.width, CH = canvas.height;
    gl.viewport(0, 0, CW, CH);
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);

    // 1) Wallpaper (fills the whole canvas, cover-fit).
    gl.disable(gl.BLEND);
    gl.useProgram(wallProg);
    gl.enableVertexAttribArray(wallProg._a.aUnit);
    gl.vertexAttribPointer(wallProg._a.aUnit, 2, gl.FLOAT, false, 0, 0);
    gl.uniform4f(U(wallProg, 'uRectClip'), -1, 1, 1, -1);
    gl.uniform2f(U(wallProg, 'uCanvas'), CW, CH);
    gl.uniform2f(U(wallProg, 'uWallSize'), wallW, wallH);
    gl.uniform1f(U(wallProg, 'uDim'), themeDim());
    gl.activeTexture(gl.TEXTURE0); gl.bindTexture(gl.TEXTURE_2D, wallTex);
    gl.uniform1i(U(wallProg, 'uWall'), 0);
    gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);

    const dark = isDark();
    const rimColor = dark ? [0.86, 0.93, 1.0] : [1.0, 1.0, 1.0];

    gl.enable(gl.BLEND);
    for (const st of surfaces.values()) {
      const rc = rectOf(st.el);
      if (!rc) continue;
      const p = st.p;
      const ox = rc.x * dpr, oy = rc.y * dpr, sw = rc.w * dpr, sh = rc.h * dpr;
      const pad = 2 * dpr;
      // Element quad in clip space (top-left origin → clip), padded for AA.
      const x0 = ((ox - pad) / CW) * 2 - 1, x1 = ((ox + sw + pad) / CW) * 2 - 1;
      const y0 = 1 - ((oy - pad) / CH) * 2, y1 = 1 - ((oy + sh + pad) / CH) * 2;
      const band = Math.min(p.refractBand * dpr, sw / 2, sh / 2);

      // Glass material (premultiplied SrcOver).
      gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
      gl.useProgram(glassProg);
      gl.enableVertexAttribArray(glassProg._a.aUnit);
      gl.vertexAttribPointer(glassProg._a.aUnit, 2, gl.FLOAT, false, 0, 0);
      gl.uniform4f(U(glassProg, 'uRectClip'), x0, y0, x1, y1);
      gl.uniform2f(U(glassProg, 'uCanvas'), CW, CH);
      gl.uniform2f(U(glassProg, 'uWallSize'), wallW, wallH);
      gl.uniform2f(U(glassProg, 'uOffset'), ox, oy);
      gl.uniform2f(U(glassProg, 'uSize'), sw, sh);
      gl.uniform1f(U(glassProg, 'uRadius'), Math.min(rc.radius * dpr, sw / 2, sh / 2));
      gl.uniform1f(U(glassProg, 'uBand'), band);
      gl.uniform1f(U(glassProg, 'uAmount'), p.refractAmount * dpr);
      gl.uniform1f(U(glassProg, 'uBlur'), p.blur * dpr);
      gl.uniform1f(U(glassProg, 'uBright'), p.brightness);
      gl.uniform1f(U(glassProg, 'uContrast'), p.contrast);
      gl.uniform1f(U(glassProg, 'uSat'), p.saturation);
      const tint = dark ? PARAMS.tintDark.slice() : PARAMS.tintLight.slice();
      if (dark && p._tintDarkA != null) tint[3] = p._tintDarkA;
      if (!dark && p._tintLightA != null) tint[3] = p._tintLightA;
      gl.uniform4f(U(glassProg, 'uTint'), tint[0], tint[1], tint[2], tint[3]);
      gl.uniform1f(U(glassProg, 'uBevel'), p.bevel);
      gl.uniform1f(U(glassProg, 'uLightAngle'), p.lightAngleDeg * Math.PI / 180);
      gl.activeTexture(gl.TEXTURE0); gl.bindTexture(gl.TEXTURE_2D, wallTex);
      gl.uniform1i(U(glassProg, 'uWall'), 0);
      gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);

      // Rim specular (additive).
      gl.blendFunc(gl.ONE, gl.ONE);
      gl.useProgram(rimProg);
      gl.enableVertexAttribArray(rimProg._a.aUnit);
      gl.vertexAttribPointer(rimProg._a.aUnit, 2, gl.FLOAT, false, 0, 0);
      gl.uniform4f(U(rimProg, 'uRectClip'), x0, y0, x1, y1);
      gl.uniform2f(U(rimProg, 'uCanvas'), CW, CH);
      gl.uniform2f(U(rimProg, 'uWallSize'), wallW, wallH);
      gl.uniform2f(U(rimProg, 'uOffset'), ox, oy);
      gl.uniform2f(U(rimProg, 'uSize'), sw, sh);
      gl.uniform1f(U(rimProg, 'uRadius'), Math.min(rc.radius * dpr, sw / 2, sh / 2));
      gl.uniform3f(U(rimProg, 'uRimColor'), rimColor[0], rimColor[1], rimColor[2]);
      gl.uniform1f(U(rimProg, 'uRimAngle'), pointerAngle);
      gl.uniform1f(U(rimProg, 'uRimFalloff'), p.rimFalloff);
      gl.uniform1f(U(rimProg, 'uRimAlpha'), p.rimAlpha * (dark ? 0.7 : 1.0));
      gl.uniform1f(U(rimProg, 'uRimStroke'), p.rimStroke * dpr);
      gl.uniform1f(U(rimProg, 'uRimBlur'), Math.max(p.rimBlur * dpr, 0.3));
      gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
    }
  }

  function loop() {
    rafId = 0;
    if (!active) return;
    const cur = signature();
    if (cur !== sig) { sig = cur; idle = 0; draw(); }
    else idle++;
    if (idle < 8) rafId = requestAnimationFrame(loop);
  }
  function wake() {
    if (!active) return;
    idle = 0; sig = '';
    if (!rafId) rafId = requestAnimationFrame(loop);
  }
  function refresh() { if (!active) return; scan(); wake(); }

  function loadWallpaper(cb) {
    const img = new Image();
    img.crossOrigin = 'anonymous';
    img.onload = () => {
      wallW = img.naturalWidth || 1440; wallH = img.naturalHeight || 900;
      wallTex = gl.createTexture();
      gl.bindTexture(gl.TEXTURE_2D, wallTex);
      gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, false);
      gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
      try { gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, img); }
      catch (e) { console.warn('[liquid-glass-gl] wallpaper upload failed:', e); return; }
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      cb();
    };
    img.onerror = () => { console.warn('[liquid-glass-gl] wallpaper load failed'); disable(); };
    img.src = WALLPAPER_URL;
  }

  function disable() {
    active = false;
    document.documentElement.removeAttribute('data-glass-gl');
    if (canvas) canvas.remove();
    cancelAnimationFrame(rafId); rafId = 0;
  }

  function init() {
    if (reducedTransparency.matches || highContrast.matches) return; // CSS fallback stays
    canvas = document.createElement('canvas');
    canvas.id = 'liquid-glass-canvas';
    canvas.setAttribute('aria-hidden', 'true');
    canvas.style.cssText = 'position:fixed;inset:0;width:100vw;height:100vh;z-index:-1;pointer-events:none;';
    // preserveDrawingBuffer:true keeps the frame readable after compositing
    // (needed for screenshot capture / pixel verification); the cost is
    // negligible for this mostly-static scene since we only redraw on change.
    const opts = { alpha: false, antialias: false, premultipliedAlpha: true, preserveDrawingBuffer: true, powerPreference: 'low-power' };
    gl = canvas.getContext('webgl', opts) || canvas.getContext('experimental-webgl', opts);
    if (!gl) return; // no WebGL → CSS fallback stays
    wallProg = program(WALL_FRAG); glassProg = program(GLASS_FRAG); rimProg = program(RIM_FRAG);
    if (!wallProg || !glassProg || !rimProg) return;
    quad = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([0, 0, 1, 0, 0, 1, 1, 1]), gl.STATIC_DRAW);

    document.body.insertBefore(canvas, document.body.firstChild);
    document.documentElement.setAttribute('data-glass-gl', 'on');
    active = true;

    loadWallpaper(() => { scan(); wake(); });

    // Wake sources.
    addEventListener('resize', wake, { passive: true });
    addEventListener('scroll', wake, { passive: true, capture: true });
    document.addEventListener('pointermove', e => {
      if (e.pointerType === 'touch') return;
      const host = e.target.closest?.('.lg-surface');
      if (host) {
        const r = host.getBoundingClientRect();
        pointerAngle = Math.atan2(e.clientY - (r.top + r.height / 2), e.clientX - (r.left + r.width / 2));
      }
      wake();
    }, { passive: true });
    const mo = new MutationObserver(() => refresh());
    mo.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['class', 'hidden', 'open', 'style'] });
    document.addEventListener('visibilitychange', () => { if (!document.hidden) refresh(); });
    matchMedia('(prefers-reduced-transparency: reduce)').addEventListener('change', v => { if (v.matches) disable(); });
    // Fonts/layout settle.
    if (document.fonts?.ready) document.fonts.ready.then(() => refresh());
    setTimeout(refresh, 300);
  }

  window.MUILiquidGlassGL = Object.freeze({
    get active() { return active; },
    refresh,
    setParams(patch) { Object.assign(PARAMS, patch || {}); surfaces.forEach(s => s.p = paramsFor(s.el)); wake(); },
    _params: PARAMS,
  });

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init, { once: true });
  else init();
})();
