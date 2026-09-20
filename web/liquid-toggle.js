/* m-ui True Liquid Glass Switch Toggle (WebGL)
 *
 * Implements Apple SwiftUI / iOS Liquid Glass switch optics & physical animation:
 * 1. Proportions & Geometry (Matching Reference & User Specification):
 *    - Pill shape consists of a center rectangle connected to two semicircular ends.
 *    - Knob center rectangle length is approximately HALF of the track center rectangle length (L_knob ≈ 0.47 * L_track).
 *    - Standard: Track 60px × 26px (rect = 34px), Knob 38px × 22px (rect = 16px ≈ 34 / 2).
 *    - Compact (switch-sm): Track 48px × 22px (rect = 26px), Knob 30px × 18px (rect = 12px ≈ 26 / 2).
 *    - Knob is always a horizontal pill capsule; it never collapses into a circle when clicked or dragged.
 *
 * 2. Synchronized Timeline (580ms Total Transit):
 *    - Morph In (230ms): Resting white capsule button swells into elevated 3D crystal liquid lens.
 *    - Glide Cruise (200ms): Cruise across the track with quintic smootherstep easing & jello stretch.
 *    - Morph Out (230ms): Deflates smoothly back into resting white capsule button upon arrival.
 *    - Track Color Transition: The track color continuously transitions between gray and vibrant green
 *      (#34c759 in light mode, #30d158 in dark mode) over the EXACT SAME 580ms duration as the slider motion.
 *
 * 3. Crystal Clear Liquid Glass Optics (Faithful to Liquid Tab Bar Shader):
 *    - Circular refractive lens mapping (circleMap) with negative optical deflection.
 *    - Track waist narrowing under the lens (sdTrackWaist), flaring back smoothly at rims.
 *    - 7-channel spectral chromatic dispersion along lens edges.
 *    - Directional specular rim highlight at -68 deg, top ridge gloss, and bottom bounce reflection.
 *    - Transparent crystal glass body in motion, morphing cleanly to/from the resting white button.
 *
 * 4. High-Performance Shared WebGL Pipeline:
 *    - Single shared offscreen WebGL renderer avoids the 16-context limit in Chromium/Edge.
 *    - Canvas blits frame via 2D drawImage (GPU texture blit).
 *    - 0% CPU/GPU overhead when switches are at rest.
 *    - 100% native accessibility, form serialization, Space/click toggling, and dragging.
 */
(() => {
  'use strict';
  if (window.MUILiquidToggleLoaded) return;
  window.MUILiquidToggleLoaded = true;

  const SELECTOR = '.switch > input[type="checkbox"], .theme-switch > input[type="checkbox"], .ultra-check > input[type="checkbox"], .check-box > input[type="checkbox"], .drawer .check > input[type="checkbox"], #client-modal .check > input[type="checkbox"]';
  const PAD = 14; // CSS pixels padding around toggle canvas for expanded lens bulge & drop shadow

  const isDark = () => document.body.classList.contains('dark') ||
                       document.documentElement.classList.contains('dark') ||
                       document.documentElement.getAttribute('data-theme') === 'dark' ||
                       document.documentElement.getAttribute('data-theme') === 'ultra-dark';

  const isRtl = el => {
    return document.documentElement.dir === 'rtl' || getComputedStyle(el).direction === 'rtl';
  };

  // Exact analytic solution of critically damped spring ODE (zeta = 1.0)
  function springStepCritical(current, velocity, target, dt, omegaN) {
    const x = current - target;
    const decay = Math.exp(-omegaN * dt);
    const offset = x * decay + (velocity + omegaN * x) * dt * decay;
    const newVel = -omegaN * x * decay + (velocity + omegaN * x) * (decay - omegaN * dt * decay);
    return { current: target + offset, velocity: newVel };
  }

  const OFFSCREEN_W = 512;
  const OFFSCREEN_H = 256;

  const VERT_SRC = `
    attribute vec2 aPos;
    void main() {
      gl_Position = vec4(aPos, 0.0, 1.0);
    }
  `;

  const FRAG_SRC = `
    precision highp float;

    uniform vec2 uCanvas;             // Total switch buffer size (including PAD) in px
    uniform vec2 uTrackCenter;        // Track center in buffer px
    uniform vec2 uTrackHalf;          // Track half-width, half-height in buffer px
    uniform float uTrackRadius;       // Track radius in buffer px
    uniform vec4 uTrackOffBg;         // Track OFF background color
    uniform vec4 uTrackOffBorder;     // Track OFF border color
    uniform vec4 uTrackOnBg;          // Track ON background color
    uniform vec4 uTrackOnBorder;      // Track ON border color
    uniform float uTrackGreenProgress;// 0.0 (gray) to 1.0 (green)

    uniform vec2 uPillCenter;         // Knob center in buffer px
    uniform vec2 uPillHalf;           // Knob half-width, half-height in buffer px
    uniform float uPillRadius;        // Knob corner radius in buffer px
    uniform float uPressProgress;     // 0.0 (resting button) .. 1.0 (3D liquid glass lens)
    uniform vec4 uRestingPill;        // Resting white pill color
    uniform float uDpr;

    float sdRoundedRect(vec2 p, vec2 b, float r) {
      vec2 q = abs(p) - (b - vec2(r));
      return length(max(q, 0.0)) - r + min(max(q.x, q.y), 0.0);
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

    float circleMap(float x) {
      return 1.0 - sqrt(max(0.0, 1.0 - x * x));
    }

    // Exact track container capsule SDF: pure, straight, uniform capsule
    float sdTrack(vec2 p) {
      float spineL = max(0.0, uTrackHalf.x - uTrackHalf.y);
      float qx = clamp(p.x - uTrackCenter.x, -spineL, spineL);
      vec2 closest = vec2(uTrackCenter.x + qx, uTrackCenter.y);
      return length(p - closest) - uTrackHalf.y;
    }

    // Apparent track inside the liquid glass slider (faithful to Apple VisionOS reference):
    // - Horizontally: optical refraction contracts the underlying track inward from both ends
    // - Vertically: optical concave waist narrowing (3.8px) in the middle body
    float sdTrackWaist(vec2 p) {
      float shrinkX = min(5.5 * uDpr, uTrackHalf.x * 0.22) * uPressProgress;
      float effTrackHalfX = max(uTrackHalf.y, uTrackHalf.x - shrinkX);
      float spineL = max(0.0, effTrackHalfX - uTrackHalf.y);
      float qx = clamp(p.x - uTrackCenter.x, -spineL, spineL);
      vec2 closest = vec2(uTrackCenter.x + qx, uTrackCenter.y);
      float distToSpine = length(p - closest);

      float u = clamp(abs(p.x - uPillCenter.x) / max(1.0, uPillHalf.x), 0.0, 1.0);
      float flare = smoothstep(0.32, 0.92, u);
      float narrowAmount = min(3.8 * uDpr, uTrackHalf.y * 0.35) * uPressProgress * (1.0 - flare);
      float effRadius = uTrackHalf.y - narrowAmount;

      return distToSpine - effRadius;
    }

    // Samples the underlying track under the liquid glass pill (faithful to liquid-tabbar.js)
    vec4 sampleScene(vec2 p) {
      float trkSd = sdTrackWaist(p);
      float fillMask = 1.0 - smoothstep(-0.8 * uDpr, 0.8 * uDpr, trkSd);
      float borderMask = 1.0 - smoothstep(0.1 * uDpr, 1.8 * uDpr, abs(trkSd));

      vec4 trkBg = mix(uTrackOffBg, uTrackOnBg, uTrackGreenProgress);
      vec4 trkBorder = mix(uTrackOffBorder, uTrackOnBorder, uTrackGreenProgress);

      float trkY = clamp((p.y - (uTrackCenter.y - uTrackHalf.y)) / max(1.0, uTrackHalf.y * 2.0), 0.0, 1.0);
      vec3 trkGrad = mix(trkBg.rgb * 1.08, trkBg.rgb * 0.92, trkY);
      vec4 scene = vec4(trkGrad, trkBg.a * fillMask);
      scene.rgb = mix(scene.rgb, trkBorder.rgb, borderMask * trkBorder.a);
      scene.a = max(scene.a, borderMask * trkBorder.a);

      return scene;
    }

    void main() {
      // Coordinate in switch buffer pixels: origin top-left
      // Viewport starts at OFFSCREEN_H - uCanvas.y, so 256.0 - gl_FragCoord.y maps top row to 0.0
      vec2 p = vec2(gl_FragCoord.x, 256.0 - gl_FragCoord.y);

      float pillSd = sdRoundedRect(p - uPillCenter, uPillHalf, uPillRadius);

      // Drop shadow under elevated floating pill
      float shadowFactor = 0.0;
      if (uPressProgress > 0.02) {
        vec2 shadowOffset = vec2(0.0, 4.5 * uDpr * uPressProgress);
        float shadowSd = sdRoundedRect(p - (uPillCenter + shadowOffset), uPillHalf, uPillRadius);
        float shadowSigma = 5.5 * uDpr * uPressProgress;
        float shadowDist = max(shadowSd, 0.0);
        if (shadowDist < 2.5 * shadowSigma && shadowSigma > 0.1) {
          float g = exp(-shadowDist * shadowDist / (2.0 * shadowSigma * shadowSigma));
          float cutoff = 1.0 - smoothstep(1.6 * shadowSigma, 2.5 * shadowSigma, shadowDist);
          shadowFactor = 0.26 * uPressProgress * g * cutoff;
        }
      } else {
        vec2 shadowOffset = vec2(0.0, 1.2 * uDpr);
        float shadowSd = sdRoundedRect(p - (uPillCenter + shadowOffset), uPillHalf, uPillRadius);
        float shadowDist = max(shadowSd, 0.0);
        if (shadowDist < 3.0 * uDpr) {
          shadowFactor = 0.10 * (1.0 - shadowDist / (3.0 * uDpr));
        }
      }

      // Base underlying scene outside the pill (straight track)
      float trkSd = sdTrack(p);
      float fillMask = 1.0 - smoothstep(-0.8 * uDpr, 0.8 * uDpr, trkSd);
      float borderMask = 1.0 - smoothstep(0.0, 1.4 * uDpr, abs(trkSd));

      vec4 trkBg = mix(uTrackOffBg, uTrackOnBg, uTrackGreenProgress);
      vec4 trkBorder = mix(uTrackOffBorder, uTrackOnBorder, uTrackGreenProgress);

      float trkY = clamp((p.y - (uTrackCenter.y - uTrackHalf.y)) / max(1.0, uTrackHalf.y * 2.0), 0.0, 1.0);
      vec3 trkGrad = mix(trkBg.rgb * 1.08, trkBg.rgb * 0.92, trkY);
      vec4 trkCol = vec4(trkGrad, trkBg.a * fillMask);
      trkCol.rgb = mix(trkCol.rgb, trkBorder.rgb, borderMask * trkBorder.a);
      trkCol.a = max(trkCol.a, borderMask * trkBorder.a);

      vec3 baseRgb = trkCol.rgb;
      float baseA = trkCol.a;

      if (shadowFactor > 0.001) {
        baseRgb = mix(baseRgb, vec3(0.0), shadowFactor);
        baseA = max(baseA, shadowFactor);
      }
      vec4 baseScene = vec4(baseRgb, baseA);

      // Outside the pill boundary: output track + shadow (alpha = 0.0 outside track/shadow)
      if (pillSd > 0.8 * uDpr) {
        gl_FragColor = vec4(baseScene.rgb * baseScene.a, baseScene.a);
        return;
      }

      // Inside or on the edge of the pill
      float pillEdgeAlpha = 1.0 - smoothstep(-0.6 * uDpr, 0.6 * uDpr, pillSd);

      // Liquid Glass Lens Optics (faithful to liquid-tabbar.js / martin65536)
      float refrHeight = min(uPillHalf.y * 0.95, 20.0 * uDpr);
      float refrAmount = 12.0 * uDpr * uPressProgress;

      float gradR = min(uPillRadius * 1.5, min(uPillHalf.x, uPillHalf.y));
      vec2 grad = gradSdRoundedRect(p - uPillCenter, uPillHalf, gradR);

      vec2 refrOffset = vec2(0.0);
      float refrInt = 0.0;
      if (pillSd < 0.0 && (-pillSd) < refrHeight) {
        float k = (-pillSd) / refrHeight;
        float edgeSmooth = smoothstep(0.0, 0.35, k);
        refrInt = (1.0 - k);
        float d = circleMap(refrInt) * refrAmount * edgeSmooth;
        refrOffset = -d * grad;
      }

      // Clean spectral chromatic dispersion along the rim
      vec4 glassSample;
      float dispInt = uPressProgress * refrInt * abs((p.x - uPillCenter.x) * (p.y - uPillCenter.y)) / max(1.0, uPillHalf.x * uPillHalf.y);
      if (dispInt > 0.003) {
        vec2 dVec = refrOffset * clamp(dispInt * 0.45, 0.0, 1.2 * uDpr);
        vec4 sRed    = sampleScene(p + dVec);
        vec4 sOrange = sampleScene(p + dVec * (2.0 / 3.0));
        vec4 sYellow = sampleScene(p + dVec * (1.0 / 3.0));
        vec4 sGreen  = sampleScene(p);
        vec4 sCyan   = sampleScene(p - dVec * (1.0 / 3.0));
        vec4 sBlue   = sampleScene(p - dVec * (2.0 / 3.0));
        vec4 sPurple = sampleScene(p - dVec);

        glassSample.r = sRed.r / 3.5 + sOrange.r / 3.5 + sYellow.r / 3.5 + sPurple.r / 7.0;
        glassSample.g = sOrange.g / 7.0 + sYellow.g / 3.5 + sGreen.g / 3.5 + sCyan.g / 3.5;
        glassSample.b = sCyan.b / 3.0 + sBlue.b / 3.0 + sPurple.b / 3.0;
        glassSample.a = (sRed.a + sGreen.a + sBlue.a) / 3.0;
      } else {
        glassSample = sampleScene(p);
      }

      vec3 glassBody = glassSample.rgb;
      float glassAlpha = glassSample.a;

      // Luminous top ridge gloss (bright ambient ceiling light)
      float topRidge = 1.0 - smoothstep(0.0, uPillHalf.y * 1.5, p.y - (uPillCenter.y - uPillHalf.y));
      float topGloss = 0.28 * uPressProgress * topRidge;
      glassBody += vec3(1.0) * topGloss;
      glassAlpha = max(glassAlpha, topGloss);

      // Subtle tangible glass body sheen (crystal clear, never milky)
      float glassSheen = 0.10 * uPressProgress * (1.0 - smoothstep(0.0, uPillHalf.y, -pillSd));
      glassBody += vec3(1.0) * glassSheen;
      glassAlpha = max(glassAlpha, glassSheen);

      // Directional specular rim highlight (light from top-left ~ -68 deg)
      float lightAngle = -68.0 * 3.14159 / 180.0;
      vec2 lightDir = vec2(cos(lightAngle), sin(lightAngle));
      float dotL = dot(grad, lightDir);
      float rimSpecular = pow(clamp(dotL, 0.0, 1.0), 2.5);
      float rimMask = 1.0 - smoothstep(0.0, 2.0 * uDpr, abs(-pillSd - 1.2 * uDpr));
      float rimAlpha = uPressProgress * rimMask * (0.95 * rimSpecular + 0.20);
      glassBody += vec3(1.0) * rimAlpha;
      glassAlpha = max(glassAlpha, rimAlpha);

      // Bottom rim bounce reflection
      float dotBottom = dot(grad, -lightDir);
      float bottomRim = uPressProgress * (1.0 - smoothstep(0.0, 2.0 * uDpr, abs(-pillSd - 1.0 * uDpr))) * 0.35 * pow(clamp(dotBottom, 0.0, 1.0), 2.0);
      glassBody += vec3(0.92, 0.96, 1.0) * bottomRim;
      glassAlpha = max(glassAlpha, bottomRim);

      // Resting pill blend: smooth morph based on pressProgress!
      // At progress = 0: pure flat resting white pill capsule!
      // At progress = 1: pure crystal transparent liquid glass!
      float restWeight = clamp(1.0 - uPressProgress * 1.6, 0.0, 1.0);
      if (restWeight > 0.001) {
        float restBorderMask = 1.0 - smoothstep(0.0, 1.0 * uDpr, abs(-pillSd - 0.6 * uDpr));
        vec3 pillCol = uRestingPill.rgb;
        float knobY = clamp((p.y - (uPillCenter.y - uPillHalf.y)) / max(1.0, uPillHalf.y * 2.0), 0.0, 1.0);
        pillCol = mix(pillCol * 1.02, pillCol * 0.98, knobY);
        pillCol = mix(pillCol, vec3(0.90, 0.92, 0.96), restBorderMask * 0.22);
        glassBody = mix(glassBody, pillCol, restWeight);
        glassAlpha = mix(glassAlpha, uRestingPill.a, restWeight);
      }

      // Smooth anti-aliased composite over base scene (clean alpha interpolation, faithful to liquid-tabbar.js)
      vec3 finalRgb = mix(baseScene.rgb, glassBody, pillEdgeAlpha);
      float finalA = mix(baseScene.a, max(baseScene.a * (1.0 - pillEdgeAlpha), glassAlpha), pillEdgeAlpha);
      gl_FragColor = vec4(finalRgb * finalA, finalA);
    }
  `;

  // Singleton Shared WebGL Renderer
  let sharedGL = null;

  function initSharedGL() {
    if (sharedGL) return sharedGL;
    const canvas = document.createElement('canvas');
    canvas.width = OFFSCREEN_W;
    canvas.height = OFFSCREEN_H;
    const opts = {
      alpha: true,
      antialias: false,
      premultipliedAlpha: true,
      powerPreference: 'high-performance',
      preserveDrawingBuffer: true
    };
    const gl = canvas.getContext('webgl', opts) || canvas.getContext('experimental-webgl', opts);
    if (!gl) {
      console.warn('[liquid-toggle] getContext webgl returned null');
      return null;
    }

    const compile = (type, src) => {
      const s = gl.createShader(type);
      gl.shaderSource(s, src);
      gl.compileShader(s);
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) {
        console.warn('[liquid-toggle] shader compile failed:', type === gl.VERTEX_SHADER ? 'VERTEX' : 'FRAGMENT', String(gl.getShaderInfoLog(s)));
        return null;
      }
      return s;
    };
    const v = compile(gl.VERTEX_SHADER, VERT_SRC);
    const f = compile(gl.FRAGMENT_SHADER, FRAG_SRC);
    if (!v || !f) return null;
    const prog = gl.createProgram();
    gl.attachShader(prog, v);
    gl.attachShader(prog, f);
    gl.linkProgram(prog);
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) {
      console.warn('[liquid-toggle] link failed:', String(gl.getProgramInfoLog(prog)));
      return null;
    }

    const quad = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW);

    const u = {};
    const n = gl.getProgramParameter(prog, gl.ACTIVE_UNIFORMS);
    for (let i = 0; i < n; i++) {
      const info = gl.getActiveUniform(prog, i);
      u[info.name] = gl.getUniformLocation(prog, info.name);
    }
    const aPos = gl.getAttribLocation(prog, 'aPos');

    sharedGL = { canvas, gl, prog, quad, u, aPos };
    window.__sharedGL = sharedGL;
    window.__controllers = controllers;
    return sharedGL;
  }

  const controllers = new Map();
  let raf = 0;
  let prevT = 0;

  function wake() {
    if (!raf) {
      prevT = 0;
      raf = requestAnimationFrame(loop);
    }
  }

  function getTargetChecked(ctrl) {
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

  // Exact proportions: L_knob ≈ 0.48 * L_track (approx 1/2)
  function measure(ctrl) {
    const shell = ctrl.shell;
    const isSmall = shell.classList.contains('switch-sm') || shell.classList.contains('lt-small') || shell.classList.contains('search-toggle');

    // Standard switch: 60px × 26px (track rect = 34px, knob rect = 16px ≈ 34 / 2)
    // Compact switch: 46px × 21px (track rect = 25px, knob rect = 12px ≈ 25 / 2)
    const tw = shell.offsetWidth || (isSmall ? 46 : 60);
    const th = shell.offsetHeight || (isSmall ? 21 : 26);

    const trackRectL = tw - th;
    const kh = th - 4; // leaves 2px margin top and bottom

    // User specification: "按钮中间的这个矩形的长度约等于外面这个轨道中间的矩形的长度的一半"
    const knobRectL = Math.round(trackRectL * 0.48);
    const kw = knobRectL + kh;

    const pad = 2;
    const travel = tw - kw - 2 * pad;

    ctrl.trackW = tw;
    ctrl.trackH = th;
    ctrl.knobW = kw;
    ctrl.knobH = kh;
    ctrl.knobRectL = knobRectL;
    ctrl.trackRectL = trackRectL;
    ctrl.pad = pad;
    ctrl.travel = travel;
    ctrl.rtl = isRtl(shell);
  }

  function draw(ctrl) {
    const shell = ctrl.shell;
    if (!shell.isConnected) return;

    if (!ctrl.trackW) measure(ctrl);
    const tw = ctrl.trackW;
    const th = ctrl.trackH;
    const dpr = Math.min(window.devicePixelRatio || 1, 2.5);

    const totalW = tw + PAD * 2;
    const totalH = th + PAD * 2;
    const W = Math.round(totalW * dpr);
    const H = Math.round(totalH * dpr);

    const canvas = ctrl.canvas;
    if (canvas.width !== W || canvas.height !== H) {
      canvas.width = W;
      canvas.height = H;
      canvas.style.width = totalW + 'px';
      canvas.style.height = totalH + 'px';
    }

    const sGL = initSharedGL();
    if (!sGL) {
      // Clean 2D fallback if WebGL unavailable
      const ctx2d = ctrl.ctx;
      ctx2d.clearRect(0, 0, W, H);
      const isChecked = ctrl.trackGreen > 0.5;
      ctx2d.save();
      ctx2d.scale(dpr, dpr);
      ctx2d.fillStyle = isChecked ? '#34c759' : 'rgba(120, 122, 128, 0.25)';
      ctx2d.beginPath();
      ctx2d.roundRect(PAD, PAD, tw, th, th / 2);
      ctx2d.fill();
      const kx = PAD + ctrl.pad + ctrl.pos * ctrl.travel;
      ctx2d.fillStyle = '#ffffff';
      ctx2d.beginPath();
      ctx2d.roundRect(kx, PAD + ctrl.pad, ctrl.knobW, ctrl.knobH, ctrl.knobH / 2);
      ctx2d.fill();
      ctx2d.restore();
      return;
    }

    const { gl, prog, quad, u, aPos } = sGL;
    gl.useProgram(prog);
    // Set viewport & scissor to top of offscreen canvas (NEVER resize WebGL canvas!)
    gl.viewport(0, OFFSCREEN_H - H, W, H);
    gl.scissor(0, OFFSCREEN_H - H, W, H);
    gl.enable(gl.SCISSOR_TEST);
    gl.clearColor(0.0, 0.0, 0.0, 0.0);
    gl.clear(gl.COLOR_BUFFER_BIT);

    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.enableVertexAttribArray(aPos);
    gl.vertexAttribPointer(aPos, 2, gl.FLOAT, false, 0, 0);

    const dark = isDark();

    // Canvas size & DPR
    gl.uniform2f(u['uCanvas'], W, H);
    gl.uniform1f(u['uDpr'], dpr);

    // Track capsule
    gl.uniform2f(u['uTrackCenter'], (PAD + tw / 2) * dpr, (PAD + th / 2) * dpr);
    gl.uniform2f(u['uTrackHalf'], (tw / 2) * dpr, (th / 2) * dpr);
    gl.uniform1f(u['uTrackRadius'], (th / 2) * dpr);

    if (dark) {
      // OFF: Translucent dark gray capsule
      gl.uniform4f(u['uTrackOffBg'], 0.50, 0.52, 0.58, 0.28);
      gl.uniform4f(u['uTrackOffBorder'], 0.72, 0.80, 0.94, 0.88);
      // ON: Apple iOS green #30d158
      gl.uniform4f(u['uTrackOnBg'], 0.188, 0.820, 0.345, 1.0);
      gl.uniform4f(u['uTrackOnBorder'], 0.25, 0.96, 0.45, 0.92);
      // Resting knob
      gl.uniform4f(u['uRestingPill'], 0.98, 0.98, 0.99, 1.0);
    } else {
      // OFF: Translucent light gray capsule
      gl.uniform4f(u['uTrackOffBg'], 0.48, 0.50, 0.54, 0.20);
      gl.uniform4f(u['uTrackOffBorder'], 0.18, 0.24, 0.35, 0.88);
      // ON: Apple iOS green #34c759
      gl.uniform4f(u['uTrackOnBg'], 0.204, 0.780, 0.349, 1.0);
      gl.uniform4f(u['uTrackOnBorder'], 0.08, 0.52, 0.20, 0.92);
      // Resting knob
      gl.uniform4f(u['uRestingPill'], 1.0, 1.0, 1.0, 1.0);
    }

    // Track green progress: 0.0 (gray) to 1.0 (green)
    gl.uniform1f(u['uTrackGreenProgress'], ctrl.trackGreen);

    // Knob sizing & inflation (maintains pill capsule aspect ratio at all times!)
    const progress = ctrl.pressProgress;

    // Resting knob dimensions:
    const restH = ctrl.knobH;
    const restW = ctrl.knobW;
    
    // Active / inflated knob dimensions:
    // Symmetrically expands outward from slider's center point, matching Apple reference elastic inflation:
    const activeH = Math.round(ctrl.trackH * 1.30); // ~34px standard, ~27px compact
    const activeW = Math.round(ctrl.trackW * 0.97); // ~58px standard (resting 38px), ~45px compact (resting 29px)
    const curBaseH = restH + (activeH - restH) * progress;
    const curBaseW = restW + (activeW - restW) * progress;

    // Velocity jello stretch along travel axis
    const speed = Math.min(Math.abs(ctrl.vx) / 800.0, 0.18);
    const pillW = curBaseW * (1.0 + speed * 1.15);
    const pillH = curBaseH / (1.0 + speed * 0.55);

    // Knob position: expands symmetrically around its center point in all directions, retaining full sliding travel
    const visualPos = ctrl.rtl ? (1.0 - ctrl.pos) : ctrl.pos;
    const curCenter = ctrl.pad + ctrl.knobW / 2 + visualPos * ctrl.travel;
    const kx = curCenter - pillW / 2;
    const ky = (ctrl.trackH - pillH) / 2;

    const pillBufX = (PAD + kx + pillW / 2) * dpr;
    const pillBufY = (PAD + ky + pillH / 2) * dpr;

    gl.uniform2f(u['uPillCenter'], pillBufX, pillBufY);
    gl.uniform2f(u['uPillHalf'], (pillW / 2) * dpr, (pillH / 2) * dpr);
    // Capsule corner radius is always half of its height
    gl.uniform1f(u['uPillRadius'], (pillH / 2) * dpr);
    gl.uniform1f(u['uPressProgress'], progress);

    gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);

    // Fast GPU blit to switch canvas
    const ctx2d = ctrl.ctx;
    ctx2d.clearRect(0, 0, W, H);
    ctx2d.drawImage(sGL.canvas, 0, 0, W, H, 0, 0, W, H);
  }

  function startTransit(ctrl, targetPos) {
    if (ctrl.transit && ctrl.transit.active && ctrl.transit.toPos === targetPos) {
      return;
    }

    measure(ctrl);
    const fromPos = (ctrl.pos !== undefined) ? ctrl.pos : (1 - targetPos);
    const fromPress = (ctrl.pressProgress !== undefined) ? ctrl.pressProgress : 0.0;
    const fromGreen = (ctrl.trackGreen !== undefined) ? ctrl.trackGreen : fromPos;
    const toGreen = targetPos;

    // Apple Liquid Glass timing:
    // 1. Morph In: 230ms flat white button -> 3D crystal liquid glass lens
    // 2. Glide: 200ms cruise across track with quintic smootherstep
    // 3. Morph Out: 230ms deflation back into flat resting button
    // Total transit duration: 580ms
    const morphInDur = 230;
    const glideStart = Math.max(0, morphInDur - 50); // 180ms
    const glideDur = 200;                            // 200ms
    const glideEnd = glideStart + glideDur;           // 380ms
    const morphOutStart = Math.max(glideStart + 30, glideEnd - 30); // 350ms
    const morphOutDur = 230;                          // 230ms
    const totalDur = morphOutStart + morphOutDur;     // 580ms

    ctrl.transit = {
      active: true,
      startTime: performance.now(),
      fromPos,
      toPos: targetPos,
      fromPress,
      fromGreen,
      toGreen,
      morphInDur,
      glideStart,
      glideDur,
      glideEnd,
      morphOutStart,
      morphOutDur,
      totalDur
    };

    ctrl.targetPos = targetPos;
    ctrl.needsDraw = true;
    wake();
  }

  function loop(now) {
    raf = 0;
    const dt = Math.min((now - (prevT || now - 16)) / 1000, 0.032);
    prevT = now;

    let anyActive = false;

    for (const ctrl of controllers.values()) {
      if (!ctrl.shell.isConnected) {
        controllers.delete(ctrl.shell);
        continue;
      }

      let moving = false;

      if (ctrl.isDragging || ctrl.isPressed) {
        // Dragging / Pressing mode: spring pressProgress to 1.0 (crystal liquid glass), 1:1 position tracking
        moving = true;
        if (ctrl.isDragging) {
          const targetX = ctrl.dragTargetPos;
          ctrl.pos = targetX;
          ctrl.trackGreen = targetX;
        }

        const rPress = springStepCritical(ctrl.pressProgress, ctrl.vPress, 1.0, dt, 24.0);
        ctrl.pressProgress = Math.max(0.0, Math.min(1.0, rPress.current));
        ctrl.vPress = rPress.velocity;

        ctrl.needsDraw = true;
      } else if (ctrl.transit && ctrl.transit.active) {
        const tr = ctrl.transit;
        const elapsed = Math.max(0, now - tr.startTime);

        // 1. Position interpolation with quintic smootherstep easing
        if (elapsed <= tr.glideStart) {
          ctrl.pos = tr.fromPos;
          ctrl.vx = 0;
        } else if (elapsed < tr.glideEnd) {
          const gu = (elapsed - tr.glideStart) / tr.glideDur;
          // Quintic smootherstep: zero 1st and 2nd derivatives at start & end
          const ease = gu * gu * gu * (gu * (gu * 6.0 - 15.0) + 10.0);
          const newPos = tr.fromPos + (tr.toPos - tr.fromPos) * ease;
          const visualDiff = (newPos - ctrl.pos) * ctrl.travel;
          ctrl.vx = dt > 0 ? visualDiff / dt : 0;
          ctrl.pos = newPos;
          moving = true;
        } else {
          ctrl.pos = tr.toPos;
          ctrl.vx = 0;
        }

        // 2. Morphing Progress (pressProgress)
        if (elapsed < tr.morphInDur) {
          // Phase 1: Swell from flat button into 3D crystal liquid glass
          const mu = elapsed / tr.morphInDur;
          const ease = mu * mu * (3.0 - 2.0 * mu);
          ctrl.pressProgress = tr.fromPress + (1.0 - tr.fromPress) * ease;
          moving = true;
        } else if (elapsed < tr.morphOutStart) {
          // Phase 2: Fully inflated liquid glass while cruising across track
          ctrl.pressProgress = 1.0;
          moving = true;
        } else if (elapsed < tr.totalDur) {
          // Phase 3: Deflate from 3D liquid glass back into flat resting button
          const ou = (elapsed - tr.morphOutStart) / tr.morphOutDur;
          const ease = ou * ou * (3.0 - 2.0 * ou);
          ctrl.pressProgress = Math.max(0.0, 1.0 - ease);
          moving = true;
        } else {
          ctrl.pressProgress = 0.0;
        }

        // 3. Track Color Transition (trackGreen)
        // STRICT REQUIREMENT: From gray to green takes the EXACT SAME time as the slider motion (totalDur = 580ms)!
        if (elapsed < tr.totalDur) {
          const tu = elapsed / tr.totalDur;
          const trackEase = tu * tu * (3.0 - 2.0 * tu);
          ctrl.trackGreen = tr.fromGreen + (tr.toGreen - tr.fromGreen) * trackEase;
          moving = true;
        } else {
          ctrl.trackGreen = tr.toGreen;
          ctrl.pos = tr.toPos;
          ctrl.pressProgress = 0.0;
          ctrl.vx = 0;
          tr.active = false;
          moving = false;
          ctrl.needsDraw = true; // One final draw to render settled resting state
        }
      } else {
        // Resting / settling verification
        if (ctrl.pressProgress > 0.001) {
          ctrl.pressProgress = Math.max(0.0, ctrl.pressProgress - dt * 4.0);
          moving = true;
        } else {
          ctrl.pressProgress = 0.0;
        }
      }

      // CRITICAL FIX: Only redraw moving controllers or controllers flagged dirty!
      if (moving || ctrl.needsDraw) {
        draw(ctrl);
        ctrl.needsDraw = false;
      }

      if (moving) anyActive = true;
    }

    if (anyActive) {
      raf = requestAnimationFrame(loop);
    } else {
      prevT = 0;
    }
  }

  function createController(shell, input) {
    if (controllers.has(shell)) return controllers.get(shell);

    // Ensure .lg-toggle class is present
    shell.classList.add('lg-toggle');
    if (shell.classList.contains('switch-sm')) shell.classList.add('lt-small');

    // Create or find the WebGL-backing 2D canvas
    let canvas = shell.querySelector(':scope > .liquid-toggle-canvas');
    if (!canvas) {
      canvas = document.createElement('canvas');
      canvas.className = 'liquid-toggle-canvas';
      canvas.setAttribute('aria-hidden', 'true');
      shell.prepend(canvas);
    }
    const ctx = canvas.getContext('2d');

    // Retain legacy track & thumb for backwards compatibility and DOM queries
    let track = shell.querySelector(':scope > .lt-track');
    if (!track) {
      track = document.createElement('span');
      track.className = 'lt-track';
      track.setAttribute('aria-hidden', 'true');
      const thumb = document.createElement('i');
      thumb.className = 'lt-thumb';
      track.appendChild(thumb);
      shell.appendChild(track);
    } else if (!track.querySelector('.lt-thumb')) {
      const thumb = document.createElement('i');
      thumb.className = 'lt-thumb';
      track.appendChild(thumb);
    }

    const checked = input ? (input.matches('input') ? input.checked : input.classList.contains('status-mode')) : false;
    const initialPos = checked ? 1.0 : 0.0;

    const ctrl = {
      shell,
      input,
      canvas,
      ctx,
      pos: initialPos,
      targetPos: initialPos,
      vx: 0,
      pressProgress: 0.0,
      vPress: 0,
      trackGreen: initialPos,
      isPressed: false,
      isDragging: false,
      dragStartX: 0,
      dragStartPos: 0,
      dragTargetPos: 0,
      transit: null
    };

    controllers.set(shell, ctrl);

    // Initial paint
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

  // Pointer Dragging & Gesture Handling
  let activePointer = null;
  let suppressState = null;

  function onPointerDown(e) {
    if (e.button !== 0 || !e.isPrimary) return;
    const shell = e.target.closest('.lg-toggle');
    if (!shell) return;
    const ctrl = controllers.get(shell);
    if (!ctrl || isDisabled(ctrl)) return;

    // If transit animation was playing, cancel it cleanly
    if (ctrl.transit) {
      ctrl.transit.active = false;
    }

    measure(ctrl);
    const startPos = ctrl.pos;
    activePointer = {
      id: e.pointerId,
      shell,
      ctrl,
      startX: e.clientX,
      startY: e.clientY,
      startPos,
      dragged: false
    };

    shell.classList.add('lt-press');
    ctrl.isPressed = true;
    ctrl.isDragging = false;
    ctrl.pressProgress = Math.max(ctrl.pressProgress, 0.4);
    ctrl.dragTargetPos = startPos;
    ctrl.needsDraw = true;
    wake();

    if (shell.setPointerCapture) {
      try { shell.setPointerCapture(e.pointerId); } catch (_) {}
    }
  }

  function onPointerMove(e) {
    if (!activePointer || e.pointerId !== activePointer.id) return;
    const p = activePointer;
    const ctrl = p.ctrl;
    if (isDisabled(ctrl)) {
      onPointerUp(e, true);
      return;
    }

    const dx = e.clientX - p.startX;
    const dy = e.clientY - p.startY;

    if (!p.dragged) {
      if (Math.abs(dy) > 6 && Math.abs(dy) > Math.abs(dx)) {
        onPointerUp(e, true);
        return;
      }
      if (Math.abs(dx) >= 3) {
        p.dragged = true;
        ctrl.isDragging = true;
        p.shell.classList.add('lt-drag');
        ctrl.pressProgress = Math.max(ctrl.pressProgress, 0.7);
        ctrl.needsDraw = true;
        wake();
      }
    }

    if (p.dragged) {
      if (e.cancelable) e.preventDefault();
      const visualDx = ctrl.rtl ? -dx : dx;
      const rawTarget = p.startPos + visualDx / Math.max(1, ctrl.travel);
      const fraction = Math.max(0.0, Math.min(1.0, rawTarget));

      // Boundary stickiness: prevent cursor from running ahead and creating a dead-zone when reversing drag!
      if (rawTarget > 1.0) {
        p.startX = e.clientX - (ctrl.rtl ? -1 : 1) * (1.0 - p.startPos) * ctrl.travel;
      } else if (rawTarget < 0.0) {
        p.startX = e.clientX - (ctrl.rtl ? -1 : 1) * (0.0 - p.startPos) * ctrl.travel;
      }

      ctrl.isDragging = true;
      ctrl.dragTargetPos = fraction;
      ctrl.pos = fraction; // immediate 1:1 finger tracking
      ctrl.trackGreen = fraction;
      ctrl.needsDraw = true;
      wake();
    }
  }

  function onPointerUp(e, cancel) {
    if (!activePointer || (e && e.pointerId !== activePointer.id)) return;
    const p = activePointer;
    activePointer = null;

    const ctrl = p.ctrl;
    ctrl.isPressed = false;
    p.shell.classList.remove('lt-press', 'lt-drag');

    if (p.shell.releasePointerCapture) {
      try { p.shell.releasePointerCapture(p.id); } catch (_) {}
    }

    if (p.dragged || cancel) {
      suppressState = { shell: p.shell, until: performance.now() + 400 };
    }

    if (p.dragged && !cancel && !isDisabled(ctrl) && ctrl.input && ctrl.input.isConnected) {
      ctrl.isDragging = false;
      const nextChecked = ctrl.dragTargetPos >= 0.5;
      const targetPos = nextChecked ? 1.0 : 0.0;
      const input = ctrl.input;
      const currentlyChecked = input.matches('input') ? input.checked : input.classList.contains('status-mode');

      if (currentlyChecked !== nextChecked) {
        input.click();
      } else {
        startTransit(ctrl, targetPos);
      }
    } else {
      ctrl.isDragging = false;
      if (cancel) {
        startTransit(ctrl, ctrl.targetPos);
      }
    }
  }

  function init() {
    scan();

    // Pointer events
    document.addEventListener('pointerdown', onPointerDown, { passive: true });
    window.addEventListener('pointermove', onPointerMove, { passive: false });
    window.addEventListener('pointerup', e => onPointerUp(e, false), { passive: true });
    window.addEventListener('pointercancel', e => onPointerUp(e, true), { passive: true });

    // Click handling: suppress trailing clicks after dragging
    document.addEventListener('click', e => {
      if (e.isTrusted && e.detail !== 0 && suppressState && performance.now() < suppressState.until && suppressState.shell.contains(e.target)) {
        e.preventDefault();
        e.stopImmediatePropagation();
        return;
      }
      const shell = e.target.closest('.lg-toggle');
      if (!shell) return;
      const ctrl = controllers.get(shell);
      if (!ctrl || isDisabled(ctrl)) return;

      // For non-input controls (e.g. button.search-toggle), change does not fire, so handle in click
      if (!ctrl.input || !ctrl.input.matches || !ctrl.input.matches('input')) {
        requestAnimationFrame(() => {
          const targetPos = getTargetChecked(ctrl) ? 1.0 : 0.0;
          startTransit(ctrl, targetPos);
        });
      }
    }, true);

    // Native change event (Space key, programmatic, accessibility)
    document.addEventListener('change', e => {
      if (!e.target.matches || !e.target.matches('input')) return;
      const shell = e.target.closest('.lg-toggle');
      if (!shell) return;
      const ctrl = controllers.get(shell);
      if (!ctrl) return;
      startTransit(ctrl, e.target.checked ? 1.0 : 0.0);
    }, true);

    // Form reset
    document.addEventListener('reset', e => {
      requestAnimationFrame(() => {
        controllers.forEach(ctrl => {
          if (ctrl.input && ctrl.input.form === e.target) {
            const targetPos = getTargetChecked(ctrl) ? 1.0 : 0.0;
            ctrl.pos = targetPos;
            ctrl.targetPos = targetPos;
            ctrl.trackGreen = targetPos;
            ctrl.pressProgress = 0.0;
            if (ctrl.transit) ctrl.transit.active = false;
            measure(ctrl);
            draw(ctrl);
          }
        });
      });
    });

    // Theme or DOM mutations
    const observer = new MutationObserver(records => {
      let needsScan = false;
      let themeChanged = false;

      for (const r of records) {
        if (r.type === 'childList') needsScan = true;
        else if (r.type === 'attributes') {
          if (r.attributeName === 'class' || r.attributeName === 'data-theme') {
            if (r.target === document.body || r.target === document.documentElement) {
              themeChanged = true;
            }
          }
          if (r.attributeName === 'class' && r.target.matches && r.target.matches('button.search-toggle.lg-toggle')) {
            const checked = String(r.target.classList.contains('status-mode'));
            if (r.target.getAttribute('aria-checked') !== checked) {
              r.target.setAttribute('aria-checked', checked);
            }
            const ctrl = controllers.get(r.target);
            const targetPos = checked === 'true' ? 1.0 : 0.0;
            if (ctrl && !ctrl.isDragging && ctrl.targetPos !== targetPos) {
              startTransit(ctrl, targetPos);
            }
          }
        }
      }

      if (needsScan) scan();

      if (themeChanged) {
        controllers.forEach(ctrl => {
          measure(ctrl);
          draw(ctrl);
        });
      }
    });

    observer.observe(document.documentElement, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ['class', 'data-theme', 'dir']
    });

    window.addEventListener('resize', () => {
      controllers.forEach(ctrl => {
        measure(ctrl);
        draw(ctrl);
      });
    }, { passive: true });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
  } else {
    init();
  }

  window.MUILiquidToggle = { controllers, draw, startTransit, measure };
})();
