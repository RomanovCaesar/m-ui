/* m-ui True Liquid Glass Tab Switch Bar (WebGL)
 *
 * Implements Apple SwiftUI / iOS Liquid Glass segmented control physics & optics:
 * 1. At Rest:
 *    - Soft neutral resting pill, perfectly centered inside the track capsule (height = trackH - 6px).
 *    - Active tab text is bolded inside resting pill, other tabs are muted.
 *    - Track is a clean, uniform, straight capsule without any artificial bumps.
 *
 * 2. Click Transition & Morphing Timing:
 *    - Sliding speed across tabs is smooth and clearly perceptible (~950ms transit, omega_n = 5.2).
 *    - Button morphing between flat button and 3D liquid glass is deliberate and visible (~450ms, omega_press = 5.5).
 *
 * 3. Crystal Clear Liquid Glass Optics:
 *    - When active, the slider is 100% transparent crystal glass (NOT an opaque white blob).
 *    - Under the lens, negative optical refraction pulls track borders inward into a narrowed waist.
 *    - At the lens rim, refraction smoothly returns to 0, connecting seamlessly with the outside straight track.
 *    - Outside the lens, the track remains completely straight and undeformed.
 *
 * 4. Spatial Optical Text Emphasis:
 *    - Under the lens, text/icons are magnified by 1.22x and sampled from the emphasized texture.
 *    - Outside the lens, text/icons remain normal weight and muted gray.
 *    - No discrete jumping: strictly whatever is spatially covered by the moving lens is emphasized.
 *
 * 5. Dragging & Elastic Rubber Band:
 *    - 1:1 pointer tracking with rubber-band resistance beyond track bounds.
 *    - Pulling past boundary pulls the entire track along by up to 7px with elastic tension.
 */
(() => {
  'use strict';
  if (window.MUILiquidTabBar) return;

  const BAR_SELECTOR = '.liquid-tabbar, .settings-tabbar, .mihomo-tabs, .inbound-status-tabs, .mihomo-editor-tabs';
  const PAD = 26; // Padding around bar in CSS pixels so inflated pill & shadow are not clipped

  // Exact analytic solution of critically damped spring ODE (zeta = 1.0)
  function springStepCritical(current, velocity, target, dt, omegaN) {
    const x = current - target;
    const decay = Math.exp(-omegaN * dt);
    const offset = x * decay + (velocity + omegaN * x) * dt * decay;
    const newVel = -omegaN * x * decay + (velocity + omegaN * x) * (decay - omegaN * dt * decay);
    return { current: target + offset, velocity: newVel };
  }

  // Exact analytic solution of underdamped spring ODE (zeta < 1.0)
  function springStepUnderdamped(current, velocity, target, dt, omegaN, dampingRatio) {
    const x0 = current - target;
    const v0 = velocity;
    const omegaD = omegaN * Math.sqrt(Math.max(0.001, 1 - dampingRatio * dampingRatio));
    const decay = Math.exp(-dampingRatio * omegaN * dt);
    const cosWd = Math.cos(omegaD * dt);
    const sinWd = Math.sin(omegaD * dt);
    const offset = x0 * decay * cosWd + ((v0 + dampingRatio * omegaN * x0) / omegaD) * decay * sinWd;
    const b0 = (v0 + dampingRatio * omegaN * x0) / omegaD;
    const newVel = -dampingRatio * omegaN * offset + decay * (-x0 * omegaD * sinWd + b0 * omegaD * cosWd);
    return { current: target + offset, velocity: newVel };
  }

  const VERT_SRC = `
    attribute vec2 aPos;
    void main() {
      gl_Position = vec4(aPos, 0.0, 1.0);
    }
  `;

  const FRAG_SRC = `
    precision highp float;

    uniform vec2 uCanvas;            // Buffer size in px (including PAD)
    uniform vec2 uTrackCenter;       // Track capsule center in buffer px
    uniform vec2 uTrackHalf;         // Track half-width, half-height in buffer px
    uniform float uTrackRadius;      // Track corner radius
    uniform vec4 uTrackBg;           // Track fill color
    uniform vec4 uTrackBorder;       // Track border color
    uniform vec4 uCardBg;            // Card background outside track

    uniform vec2 uPillCenter;        // Pill center in buffer px
    uniform vec2 uPillHalf;          // Pill half-width, half-height in buffer px
    uniform float uPillRadius;       // Pill corner radius
    uniform float uPressProgress;    // 0.0 at rest .. 1.0 fully pressed/dragged
    uniform vec4 uRestingPill;       // Soft resting pill color
    uniform float uDpr;

    uniform sampler2D uLabelsNormalTex; // Normal weight, muted text & icons
    uniform sampler2D uLabelsEmphTex;   // Emphasized weight, active text & icons

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
    // - Horizontally: optical refraction contracts the track cap inward when the lens is near either end
    // - Vertically: optical concave waist narrowing (3.6px) along the slider body
    float sdTrackWaist(vec2 p) {
      float spineL = max(0.0, uTrackHalf.x - uTrackHalf.y);
      float shrinkMax = min(5.5 * uDpr, uTrackHalf.y * 0.35) * uPressProgress;

      float leftEdgeDist = max(0.0, (uPillCenter.x - uPillHalf.x) - (uTrackCenter.x - uTrackHalf.x));
      float rightEdgeDist = max(0.0, (uTrackCenter.x + uTrackHalf.x) - (uPillCenter.x + uPillHalf.x));
      float leftCov = 1.0 - smoothstep(0.0, max(24.0 * uDpr, uPillHalf.x * 1.2), leftEdgeDist);
      float rightCov = 1.0 - smoothstep(0.0, max(24.0 * uDpr, uPillHalf.x * 1.2), rightEdgeDist);

      float spineLeft = -spineL + shrinkMax * leftCov;
      float spineRight = spineL - shrinkMax * rightCov;
      float qx = clamp(p.x - uTrackCenter.x, spineLeft, spineRight);
      vec2 closest = vec2(uTrackCenter.x + qx, uTrackCenter.y);
      float distToSpine = length(p - closest);

      float u = clamp(abs(p.x - uPillCenter.x) / max(1.0, uPillHalf.x), 0.0, 1.0);
      float flare = smoothstep(0.40, 0.95, u);
      float narrowAmount = 3.6 * uDpr * uPressProgress * (1.0 - flare);
      float effRadius = uTrackHalf.y - narrowAmount;

      return distToSpine - effRadius;
    }

    // Samples the underlying track and emphasized labels under the liquid glass pill
    vec4 sampleScene(vec2 p, float magnify, vec2 pRefracted) {
      float trkSd = sdTrackWaist(p);
      float fillMask = 1.0 - smoothstep(-0.8 * uDpr, 0.8 * uDpr, trkSd);
      float borderMask = 1.0 - smoothstep(0.0, 1.4 * uDpr, abs(trkSd));

      // Translucent track fill and crisp visible border
      float trkY = clamp((p.y - (uTrackCenter.y - uTrackHalf.y)) / max(1.0, uTrackHalf.y * 2.0), 0.0, 1.0);
      vec3 trkGrad = mix(uTrackBg.rgb * 1.10, uTrackBg.rgb * 0.90, trkY);
      vec4 scene = vec4(trkGrad, uTrackBg.a * fillMask);
      scene.rgb = mix(scene.rgb, uTrackBorder.rgb, borderMask * uTrackBorder.a);
      scene.a = max(scene.a, borderMask * uTrackBorder.a);

      // Sample emphasized tab labels under the lens (premultiplied Over blend, magnified + refracted)
      vec2 textCoord = uPillCenter + (pRefracted - uPillCenter) / magnify;
      vec2 uv = vec2(textCoord.x / uCanvas.x, textCoord.y / uCanvas.y);
      if (uv.x >= 0.0 && uv.x <= 1.0 && uv.y >= 0.0 && uv.y <= 1.0) {
        vec4 lbl = texture2D(uLabelsEmphTex, uv);
        scene.rgb = scene.rgb * (1.0 - lbl.a) + lbl.rgb;
        scene.a = max(scene.a, lbl.a);
      }
      return scene;
    }

    void main() {
      vec2 p = vec2(gl_FragCoord.x, uCanvas.y - gl_FragCoord.y);

      float pillSd = sdRoundedRect(p - uPillCenter, uPillHalf, uPillRadius);

      // Drop shadow under elevated pill
      float shadowFactor = 0.0;
      if (uPressProgress > 0.02) {
        vec2 shadowOffset = vec2(0.0, 5.0 * uDpr * uPressProgress);
        float shadowSd = sdRoundedRect(p - (uPillCenter + shadowOffset), uPillHalf, uPillRadius);
        float shadowSigma = 6.0 * uDpr * uPressProgress;
        float shadowDist = max(shadowSd, 0.0);
        if (shadowDist < 2.5 * shadowSigma && shadowSigma > 0.1) {
          float g = exp(-shadowDist * shadowDist / (2.0 * shadowSigma * shadowSigma));
          float cutoff = 1.0 - smoothstep(1.6 * shadowSigma, 2.5 * shadowSigma, shadowDist);
          shadowFactor = 0.28 * uPressProgress * g * cutoff;
        }
      } else {
        vec2 shadowOffset = vec2(0.0, 1.2 * uDpr);
        float shadowSd = sdRoundedRect(p - (uPillCenter + shadowOffset), uPillHalf, uPillRadius);
        float shadowDist = max(shadowSd, 0.0);
        if (shadowDist < 3.0 * uDpr) {
          shadowFactor = 0.07 * (1.0 - shadowDist / (3.0 * uDpr));
        }
      }

      // Base underlying scene: straight track + normal labels outside the pill
      float trkSd = sdTrack(p);
      float fillMask = 1.0 - smoothstep(-0.8 * uDpr, 0.8 * uDpr, trkSd);
      float borderMask = 1.0 - smoothstep(0.0, 1.4 * uDpr, abs(trkSd));

      float trkY = clamp((p.y - (uTrackCenter.y - uTrackHalf.y)) / max(1.0, uTrackHalf.y * 2.0), 0.0, 1.0);
      vec3 trkGrad = mix(uTrackBg.rgb * 1.08, uTrackBg.rgb * 0.92, trkY);
      vec4 trkCol = vec4(trkGrad, uTrackBg.a * fillMask);
      trkCol.rgb = mix(trkCol.rgb, uTrackBorder.rgb, borderMask * uTrackBorder.a);
      trkCol.a = max(trkCol.a, borderMask * uTrackBorder.a);

      // Mask out unrefracted normal labels under the pill to prevent any ghosting
      float pillInteriorMask = smoothstep(-1.2 * uDpr, 0.6 * uDpr, pillSd);
      vec2 normalUv = p / uCanvas;
      vec4 normalLbl = texture2D(uLabelsNormalTex, normalUv) * pillInteriorMask;
      vec3 baseRgb = trkCol.rgb * (1.0 - normalLbl.a) + normalLbl.rgb;
      float baseA = max(trkCol.a, normalLbl.a);

      if (shadowFactor > 0.001) {
        baseRgb = mix(baseRgb, vec3(0.0), shadowFactor * (1.0 - normalLbl.a));
        baseA = max(baseA, shadowFactor);
      }
      vec4 baseScene = vec4(baseRgb, baseA);

      // Outside the pill
      if (pillSd > 0.8 * uDpr) {
        gl_FragColor = vec4(baseScene.rgb * baseScene.a, baseScene.a);
        return;
      }

      // Inside or on the edge of the pill
      float pillEdgeAlpha = 1.0 - smoothstep(-0.6 * uDpr, 0.6 * uDpr, pillSd);

      // Liquid glass refraction setup (faithful to martin65536/liquid-glass-webgl)
      float refrHeight = min(uPillHalf.y * 0.95, 22.0 * uDpr);
      float refrAmount = 14.0 * uDpr * uPressProgress;

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

      vec2 sampleP = p + refrOffset;
      float magnify = 1.0 + 0.22 * uPressProgress; // 1.22x magnification under the lens

      // Subtle, clean spectral chromatic dispersion along the rim (no text doubling)
      vec4 glassSample;
      float dispInt = uPressProgress * refrInt * abs((p.x - uPillCenter.x) * (p.y - uPillCenter.y)) / max(1.0, uPillHalf.x * uPillHalf.y);
      if (dispInt > 0.003) {
        vec2 dVec = refrOffset * clamp(dispInt * 0.45, 0.0, 1.2 * uDpr);
        vec4 sRed    = sampleScene(p, magnify, sampleP + dVec);
        vec4 sOrange = sampleScene(p, magnify, sampleP + dVec * (2.0 / 3.0));
        vec4 sYellow = sampleScene(p, magnify, sampleP + dVec * (1.0 / 3.0));
        vec4 sGreen  = sampleScene(p, magnify, sampleP);
        vec4 sCyan   = sampleScene(p, magnify, sampleP - dVec * (1.0 / 3.0));
        vec4 sBlue   = sampleScene(p, magnify, sampleP - dVec * (2.0 / 3.0));
        vec4 sPurple = sampleScene(p, magnify, sampleP - dVec);

        glassSample.r = sRed.r / 3.5 + sOrange.r / 3.5 + sYellow.r / 3.5 + sPurple.r / 7.0;
        glassSample.g = sOrange.g / 7.0 + sYellow.g / 3.5 + sGreen.g / 3.5 + sCyan.g / 3.5;
        glassSample.b = sCyan.b / 3.0 + sBlue.b / 3.0 + sPurple.b / 3.0;
        glassSample.a = (sRed.a + sGreen.a + sBlue.a) / 3.0;
      } else {
        glassSample = sampleScene(p, magnify, sampleP);
      }

      vec3 glassBody = glassSample.rgb;
      float glassAlpha = glassSample.a;

      // Luminous top ridge gloss (bright ambient ceiling light)
      float topRidge = 1.0 - smoothstep(0.0, uPillHalf.y * 1.5, p.y - (uPillCenter.y - uPillHalf.y));
      float topGloss = 0.28 * uPressProgress * topRidge;
      glassBody += vec3(1.0) * topGloss;
      glassAlpha = max(glassAlpha, topGloss);

      // Subtle tangible glass body sheen
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
      // At progress = 0: pure flat resting pill
      // At progress = 1: pure crystal transparent liquid glass!
      float restWeight = clamp(1.0 - uPressProgress * 1.6, 0.0, 1.0);
      if (restWeight > 0.001) {
        float restBorderMask = 1.0 - smoothstep(0.0, 1.0 * uDpr, abs(-pillSd - 0.6 * uDpr));
        vec3 pillCol = uRestingPill.rgb;
        if (uTrackBorder.a > 0.0) {
          pillCol = mix(pillCol, uTrackBorder.rgb, restBorderMask * 0.22);
        }
        // In resting state, blend resting pill background behind the text without re-drawing text
        glassBody = mix(glassBody, pillCol, restWeight * (1.0 - glassSample.a * 0.7));
        glassAlpha = mix(glassAlpha, uRestingPill.a, restWeight);
      }

      // Smooth anti-aliased composite over base scene (clean alpha interpolation)
      vec3 finalRgb = mix(baseScene.rgb, glassBody, pillEdgeAlpha);
      float finalA = mix(baseScene.a, max(baseScene.a * (1.0 - pillEdgeAlpha), glassAlpha), pillEdgeAlpha);
      gl_FragColor = vec4(finalRgb * finalA, finalA);
    }
  `;

  const isDark = () => document.body.classList.contains('dark') || document.documentElement.classList.contains('dark');
  const controllers = new Map();
  const pathCache = new Map();

  function getSymbolPaths(symbolId) {
    if (!symbolId) return null;
    if (pathCache.has(symbolId)) return pathCache.get(symbolId);
    const sym = document.getElementById(symbolId);
    if (!sym) return null;
    const vb = (sym.getAttribute('viewBox') || '0 0 1024 1024').trim().split(/[\s,]+/).map(Number);
    const vbX = vb[0] || 0, vbY = vb[1] || 0, vbW = vb[2] || 1024, vbH = vb[3] || 1024;
    const paths = [];
    for (const p of sym.querySelectorAll('path')) {
      const d = p.getAttribute('d');
      if (d) paths.push(new Path2D(d));
    }
    const data = { vbX, vbY, vbW, vbH, paths };
    pathCache.set(symbolId, data);
    return data;
  }

  function getEffectiveBg(el) {
    let cur = el ? el.parentElement : null;
    while (cur && cur !== document.documentElement) {
      const bg = getComputedStyle(cur).backgroundColor;
      if (bg && bg !== 'transparent' && !bg.startsWith('rgba(0, 0, 0, 0)')) {
        const m = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)/);
        if (m) {
          const a = m[4] !== undefined ? parseFloat(m[4]) : 1.0;
          if (a > 0.1) return [m[1] / 255, m[2] / 255, m[3] / 255, a];
        }
      }
      cur = cur.parentElement;
    }
    return isDark() ? [0.10, 0.13, 0.18, 1.0] : [0.976, 0.984, 1.0, 1.0];
  }

  function makeGL(canvas) {
    const opts = {
      alpha: true,
      antialias: false,
      premultipliedAlpha: true,
      powerPreference: 'high-performance',
      preserveDrawingBuffer: true
    };
    const gl = canvas.getContext('webgl', opts) || canvas.getContext('experimental-webgl', opts);
    if (!gl) return null;

    const compile = (type, src) => {
      const s = gl.createShader(type); gl.shaderSource(s, src); gl.compileShader(s);
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) { console.warn('[liquid-tabbar] shader:', gl.getShaderInfoLog(s)); return null; }
      return s;
    };
    const v = compile(gl.VERTEX_SHADER, VERT_SRC);
    const f = compile(gl.FRAGMENT_SHADER, FRAG_SRC);
    if (!v || !f) return null;
    const prog = gl.createProgram();
    gl.attachShader(prog, v); gl.attachShader(prog, f); gl.linkProgram(prog);
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) { console.warn('[liquid-tabbar] link:', gl.getProgramInfoLog(prog)); return null; }

    const quad = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW);

    const labelsNormalTex = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, labelsNormalTex);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);

    const labelsEmphTex = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, labelsEmphTex);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);

    const u = {};
    const n = gl.getProgramParameter(prog, gl.ACTIVE_UNIFORMS);
    for (let i = 0; i < n; i++) {
      const info = gl.getActiveUniform(prog, i);
      u[info.name] = gl.getUniformLocation(prog, info.name);
    }
    const aPos = gl.getAttribLocation(prog, 'aPos');

    return { gl, prog, quad, labelsNormalTex, labelsEmphTex, u, aPos };
  }

  function buttonsOf(bar) {
    return Array.from(bar.querySelectorAll('button')).filter(b =>
      !b.hidden && !b.disabled && b.offsetWidth > 0);
  }

  function hideDomButtons(bar) {
    if (!bar) return;
    for (const b of bar.querySelectorAll('button')) {
      b.style.setProperty('opacity', '0', 'important');
      b.style.setProperty('color', 'transparent', 'important');
      b.style.setProperty('-webkit-text-fill-color', 'transparent', 'important');
      b.style.setProperty('text-shadow', 'none', 'important');
      b.style.setProperty('background', 'transparent', 'important');
      b.style.setProperty('border-color', 'transparent', 'important');
      b.style.setProperty('box-shadow', 'none', 'important');
      for (const child of b.querySelectorAll('*')) {
        child.style.setProperty('opacity', '0', 'important');
        child.style.setProperty('color', 'transparent', 'important');
        child.style.setProperty('-webkit-text-fill-color', 'transparent', 'important');
      }
    }
  }

  function activeBtnOf(bar) {
    const list = buttonsOf(bar);
    return list.find(b => b.classList.contains('active') || b.getAttribute('aria-selected') === 'true') || list[0];
  }

  // Dual-texture rasterization:
  // 1. Normal texture (weight 500, muted gray): sampled outside the pill lens.
  // 2. Emphasized texture (weight 600, active color): sampled inside the pill lens.
  // Rasterized ONLY once on layout / resize / theme change, never per-frame!
  function updateLabelsTexture(ctrl, W, H, dpr) {
    const bar = ctrl.bar;
    const dark = isDark();
    const buttons = buttonsOf(bar);
    hideDomButtons(bar);

    const cacheKey = `${dark}_${W}_${H}_${dpr}_${buttons.length}`;
    if (ctrl.labelsKey === cacheKey) return;
    ctrl.labelsKey = cacheKey;

    const barRect = bar.getBoundingClientRect();

    // 1. Normal raster (weight 500, muted color)
    const rNormal = ctrl.rasterNormal || (ctrl.rasterNormal = document.createElement('canvas'));
    if (rNormal.width !== W || rNormal.height !== H) {
      rNormal.width = W; rNormal.height = H;
    }
    const ctxNorm = rNormal.getContext('2d');
    ctxNorm.clearRect(0, 0, W, H);
    ctxNorm.setTransform(dpr, 0, 0, dpr, 0, 0);

    // 2. Emphasized raster (weight 600, high contrast active color)
    const rEmph = ctrl.rasterEmph || (ctrl.rasterEmph = document.createElement('canvas'));
    if (rEmph.width !== W || rEmph.height !== H) {
      rEmph.width = W; rEmph.height = H;
    }
    const ctxEmph = rEmph.getContext('2d');
    ctxEmph.clearRect(0, 0, W, H);
    ctxEmph.setTransform(dpr, 0, 0, dpr, 0, 0);

    const isAirplane = bar.id === 'airplane-tabs';
    const normColor = isAirplane ? '#000000' : (dark ? '#94a3b8' : '#58667a');
    const emphColor = isAirplane ? '#0088ff' : (dark ? '#ffffff' : '#0f172a');

    for (const btn of buttons) {
      const bRect = btn.getBoundingClientRect();
      const relX = PAD + (bRect.left - barRect.left);
      const relY = PAD + (bRect.top - barRect.top);
      const btnW = bRect.width;
      const btnH = bRect.height;

      const cs = getComputedStyle(btn);
      const family = cs.fontFamily || '-apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif';
      const size = cs.fontSize || '13.5px';

      // SVG icon if present (supports <use href="#sf-..."> and inline <svg> with <path>/<circle>)
      const svg = btn.querySelector('svg');
      let symData = null, iconX = 0, iconY = 0, iconW = 0, iconH = 0;
      if (svg) {
        const svgRect = svg.getBoundingClientRect();
        const use = svg.querySelector('use');
        const href = use ? (use.getAttribute('href') || use.getAttribute('xlink:href')) : '';
        const symbolId = href ? href.replace(/^#/, '') : '';
        symData = getSymbolPaths(symbolId);
        if (!symData) {
          // Fallback for inline SVGs: parse paths & circles directly from svg element!
          const vb = (svg.getAttribute('viewBox') || '0 0 24 24').trim().split(/[\s,]+/).map(Number);
          const vbX = vb[0] || 0, vbY = vb[1] || 0, vbW = vb[2] || 24, vbH = vb[3] || 24;
          const paths = [];
          for (const p of svg.querySelectorAll('path')) {
            const d = p.getAttribute('d');
            if (d) paths.push(new Path2D(d));
          }
          for (const c of svg.querySelectorAll('circle')) {
            const cx = parseFloat(c.getAttribute('cx') || 0);
            const cy = parseFloat(c.getAttribute('cy') || 0);
            const r = parseFloat(c.getAttribute('r') || 0);
            const p = new Path2D();
            p.arc(cx, cy, r, 0, Math.PI * 2);
            paths.push(p);
          }
          if (paths.length) symData = { vbX, vbY, vbW, vbH, paths };
        }
        iconX = PAD + (svgRect.left - barRect.left);
        iconY = PAD + (svgRect.top - barRect.top);
        iconW = svgRect.width;
        iconH = svgRect.height;
      }

      // Text label
      const text = (btn.textContent || '').trim();
      let textX = relX + btnW / 2;
      let textAlign = 'center';
      if (text && svg) {
        let textNode = null;
        for (const n of btn.childNodes) {
          if (n.nodeType === Node.TEXT_NODE && n.textContent.trim()) { textNode = n; break; }
          if (n.nodeType === Node.ELEMENT_NODE && !n.matches('svg, .sf-symbol') && n.textContent.trim()) {
            for (const cn of n.childNodes) if (cn.nodeType === Node.TEXT_NODE && cn.textContent.trim()) { textNode = cn; break; }
          }
        }
        if (textNode) {
          const range = document.createRange();
          range.selectNodeContents(textNode);
          const tr = range.getBoundingClientRect();
          textAlign = 'left';
          textX = PAD + (tr.left - barRect.left);
        } else {
          textX = relX + btnW / 2 + 8;
        }
      }

      // Render onto Normal canvas (weight 500, muted color)
      if (symData && symData.paths.length) {
        ctxNorm.save();
        ctxNorm.translate(iconX, iconY);
        ctxNorm.scale(iconW / symData.vbW, iconH / symData.vbH);
        ctxNorm.translate(-symData.vbX, -symData.vbY);
        ctxNorm.fillStyle = normColor;
        for (const p of symData.paths) ctxNorm.fill(p);
        ctxNorm.restore();
      }
      if (text) {
        ctxNorm.font = `500 ${size} ${family}`;
        ctxNorm.fillStyle = normColor;
        ctxNorm.textBaseline = 'middle';
        ctxNorm.textAlign = textAlign;
        ctxNorm.fillText(text, textX, relY + btnH / 2);
      }

      // Render onto Emphasized canvas (weight 600, high contrast color)
      if (symData && symData.paths.length) {
        ctxEmph.save();
        ctxEmph.translate(iconX, iconY);
        ctxEmph.scale(iconW / symData.vbW, iconH / symData.vbH);
        ctxEmph.translate(-symData.vbX, -symData.vbY);
        ctxEmph.fillStyle = emphColor;
        for (const p of symData.paths) ctxEmph.fill(p);
        ctxEmph.restore();
      }
      if (text) {
        ctxEmph.font = `600 ${size} ${family}`;
        ctxEmph.fillStyle = emphColor;
        ctxEmph.textBaseline = 'middle';
        ctxEmph.textAlign = textAlign;
        ctxEmph.fillText(text, textX, relY + btnH / 2);
      }
    }

    const { gl, labelsNormalTex, labelsEmphTex } = ctrl;
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);

    gl.bindTexture(gl.TEXTURE_2D, labelsNormalTex);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, rNormal);

    gl.bindTexture(gl.TEXTURE_2D, labelsEmphTex);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, rEmph);
  }

  function draw(ctrl) {
    const bar = ctrl.bar;
    if (!bar.isConnected) return;

    const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
    const barW = bar.offsetWidth;
    const barH = bar.offsetHeight;
    if (barW < 2 || barH < 2) return;

    const totalW = barW + PAD * 2;
    const totalH = barH + PAD * 2;
    const W = Math.round(totalW * dpr);
    const H = Math.round(totalH * dpr);

    const canvas = ctrl.canvas;
    if (canvas.width !== W || canvas.height !== H) {
      canvas.width = W; canvas.height = H;
      canvas.style.width = totalW + 'px';
      canvas.style.height = totalH + 'px';
    }

    updateLabelsTexture(ctrl, W, H, dpr);

    const { gl, prog, quad, labelsNormalTex, labelsEmphTex, u, aPos } = ctrl;
    gl.viewport(0, 0, W, H);
    gl.useProgram(prog);

    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.enableVertexAttribArray(aPos);
    gl.vertexAttribPointer(aPos, 2, gl.FLOAT, false, 0, 0);

    gl.activeTexture(gl.TEXTURE0);
    gl.bindTexture(gl.TEXTURE_2D, labelsNormalTex);
    gl.uniform1i(u['uLabelsNormalTex'], 0);

    gl.activeTexture(gl.TEXTURE1);
    gl.bindTexture(gl.TEXTURE_2D, labelsEmphTex);
    gl.uniform1i(u['uLabelsEmphTex'], 1);

    const dark = isDark();

    // Canvas size & DPR
    gl.uniform2f(u['uCanvas'], W, H);
    gl.uniform1f(u['uDpr'], dpr);

    // Track capsule (in buffer coords)
    gl.uniform2f(u['uTrackCenter'], (PAD + barW / 2) * dpr, (PAD + barH / 2) * dpr);
    gl.uniform2f(u['uTrackHalf'], (barW / 2) * dpr, (barH / 2) * dpr);
    gl.uniform1f(u['uTrackRadius'], (barH / 2) * dpr);

    // Card and Track Colors
    const cardBg = getEffectiveBg(bar);
    gl.uniform4f(u['uCardBg'], cardBg[0], cardBg[1], cardBg[2], cardBg[3]);

    if (bar.id === 'airplane-tabs') {
      gl.uniform4f(u['uTrackBg'], 0.20, 0.65, 0.90, 0.28);
      gl.uniform4f(u['uTrackBorder'], 1.0, 1.0, 1.0, 0.70);
      gl.uniform4f(u['uRestingPill'], 0.40, 0.78, 0.95, 0.35);
    } else if (dark) {
      gl.uniform4f(u['uTrackBg'], 0.60, 0.70, 0.85, 0.18);
      gl.uniform4f(u['uTrackBorder'], 0.80, 0.88, 1.0, 0.60);
      gl.uniform4f(u['uRestingPill'], 0.24, 0.28, 0.36, 0.96);
    } else {
      gl.uniform4f(u['uTrackBg'], 0.40, 0.50, 0.62, 0.14);
      gl.uniform4f(u['uTrackBorder'], 0.22, 0.32, 0.46, 0.68);
      gl.uniform4f(u['uRestingPill'], 1.0, 1.0, 1.0, 0.96);
    }

    // Pill geometry & scale inflation
    const progress = ctrl.pressProgress;

    // Sizing:
    // At rest (progress = 0): pill height is (barH - 6), fitting neatly inside the track.
    // When active / dragged / clicked (progress = 1.0): pill height expands to (barH + 12),
    // clearly exceeding the track height by 6px above and 6px below!
    const restH = Math.max(24, barH - 6);
    const activeH = barH + 12;
    const curBaseH = restH + (activeH - restH) * progress;

    const restW = ctrl.posW;
    const activeW = ctrl.posW + 8;
    const curBaseW = restW + (activeW - restW) * progress;

    // Jello stretch along travel axis
    const speed = Math.min(Math.abs(ctrl.vx) / 1200.0, 0.16);
    const pillW = curBaseW * (1.0 + speed * 1.2);
    const pillH = curBaseH / (1.0 + speed * 0.6);

    const pillX = ctrl.posX + (ctrl.posW - pillW) / 2;
    const pillY = (barH - pillH) / 2;

    const pillBufX = (PAD + pillX + pillW / 2) * dpr;
    const pillBufY = (PAD + pillY + pillH / 2) * dpr;

    gl.uniform2f(u['uPillCenter'], pillBufX, pillBufY);
    gl.uniform2f(u['uPillHalf'], (pillW / 2) * dpr, (pillH / 2) * dpr);
    gl.uniform1f(u['uPillRadius'], (pillH / 2) * dpr);
    gl.uniform1f(u['uPressProgress'], progress);

    gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
  }

  function startTransit(ctrl, targetBtn) {
    if (!targetBtn) return;
    const bar = ctrl.bar;
    hideDomButtons(bar);
    const barRect = bar.getBoundingClientRect();
    const r = targetBtn.getBoundingClientRect();
    const toX = r.left - barRect.left + 1;
    const toW = r.width - 2;

    const fromX = (ctrl.posX !== undefined) ? ctrl.posX : toX;
    const fromW = (ctrl.posW !== undefined) ? ctrl.posW : toW;
    const fromPress = (ctrl.pressProgress !== undefined) ? ctrl.pressProgress : 0.0;

    ctrl.targetBtn = targetBtn;
    ctrl.candidateBtn = targetBtn;

    // User-tuned Apple Liquid Glass timing:
    // 1. Morph In: 230ms flat button -> 3D liquid glass lens
    // 2. Glide: 200ms fast cruise across tabs
    // 3. Morph Out: 230ms deflation back to flat resting button
    const morphInDur = Math.round(Math.max(60, (1.0 - fromPress) * 230));
    const glideStart = Math.max(0, morphInDur - 50);
    const glideDur = 200;
    const glideEnd = glideStart + glideDur;
    const morphOutStart = Math.max(glideStart + 30, glideEnd - 30);
    const morphOutDur = 230;
    const totalDur = morphOutStart + morphOutDur;

    ctrl.transit = {
      active: true,
      startTime: performance.now(),
      fromX,
      fromW,
      toX,
      toW,
      fromPress,
      morphInDur,
      glideStart,
      glideDur,
      glideEnd,
      morphOutStart,
      morphOutDur,
      totalDur
    };

    ctrl.isTransit = true;
    ctrl.targetPress = 1.0;
    wake(ctrl);
  }

  function loop(ctrl, now) {
    ctrl.raf = 0;
    if (!ctrl.gl) return;

    const dt = Math.min((now - (ctrl.prev || now - 16)) / 1000, 0.032);
    ctrl.prev = now;

    const bar = ctrl.bar;
    const barRect = bar.getBoundingClientRect();
    const buttons = buttonsOf(bar);

    let moving = false;

    if (ctrl.isDragging || ctrl.isPressed) {
      moving = true;
      const targetX = ctrl.isDragging ? ctrl.dragCurrX : (ctrl.targetBtn ? (ctrl.targetBtn.getBoundingClientRect().left - barRect.left + 1) : ctrl.posX);
      const targetW = ctrl.candidateBtn ? (ctrl.candidateBtn.offsetWidth - 2) : ctrl.posW;

      const OMEGA_X = 36.0;
      const rx = springStepCritical(ctrl.posX, ctrl.vx, targetX, dt, OMEGA_X);
      ctrl.posX = rx.current;
      ctrl.vx = rx.velocity;
      if (Math.abs(ctrl.posX - targetX) > 0.08 || Math.abs(ctrl.vx) > 0.2) moving = true;
      else { ctrl.posX = targetX; ctrl.vx = 0; }

      const rw = springStepCritical(ctrl.posW, ctrl.vw, targetW, dt, OMEGA_X);
      ctrl.posW = rw.current;
      ctrl.vw = rw.velocity;
      if (Math.abs(ctrl.posW - targetW) > 0.08 || Math.abs(ctrl.vw) > 0.2) moving = true;
      else { ctrl.posW = targetW; ctrl.vw = 0; }

      const rPress = springStepCritical(ctrl.pressProgress, ctrl.vPress, 1.0, dt, 24.0);
      ctrl.pressProgress = Math.max(0.0, Math.min(1.0, rPress.current));
      ctrl.vPress = rPress.velocity;
      if (Math.abs(ctrl.pressProgress - 1.0) > 0.01) moving = true;
    } else if (ctrl.transit && ctrl.transit.active) {
      const tr = ctrl.transit;
      const elapsed = Math.max(0, now - tr.startTime);

      // 1. Position & Width interpolation with quintic smootherstep easing
      if (elapsed <= tr.glideStart) {
        ctrl.posX = tr.fromX;
        ctrl.posW = tr.fromW;
        ctrl.vx = 0;
      } else if (elapsed < tr.glideEnd) {
        const gu = (elapsed - tr.glideStart) / tr.glideDur;
        // Quintic smootherstep: starts and ends with zero velocity and acceleration
        const ease = gu * gu * gu * (gu * (gu * 6.0 - 15.0) + 10.0);
        const newX = tr.fromX + (tr.toX - tr.fromX) * ease;
        const newW = tr.fromW + (tr.toW - tr.fromW) * ease;
        ctrl.vx = dt > 0 ? (newX - ctrl.posX) / dt : 0;
        ctrl.posX = newX;
        ctrl.posW = newW;
        moving = true;
      } else {
        ctrl.posX = tr.toX;
        ctrl.posW = tr.toW;
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
        // Transit complete!
        ctrl.pressProgress = 0.0;
        ctrl.targetPress = 0.0;
        ctrl.isTransit = false;
        tr.active = false;
        moving = false;

        // Confirm target button as active upon arrival
        if (ctrl.targetBtn) {
          ctrl.activeBtn = ctrl.targetBtn;
          ctrl.suppressMutation = true;
          buttons.forEach(b => {
            const shouldBeActive = (b === ctrl.targetBtn);
            if (b.classList.contains('active') !== shouldBeActive) {
              b.classList.toggle('active', shouldBeActive);
            }
          });
          Promise.resolve().then(() => { ctrl.suppressMutation = false; });
        }
      }
    } else {
      // Idle or spring-back state: snap to active button if valid
      const b = ctrl.activeBtn || activeBtnOf(bar);
      if (b) {
        const r = b.getBoundingClientRect();
        const targetX = r.left - barRect.left + 1;
        const targetW = r.width - 2;
        if (ctrl.posX === undefined) {
          ctrl.posX = targetX;
          ctrl.posW = targetW;
        } else {
          const rx = springStepCritical(ctrl.posX, ctrl.vx, targetX, dt, 36.0);
          ctrl.posX = rx.current;
          ctrl.vx = rx.velocity;
          const rw = springStepCritical(ctrl.posW, ctrl.vw, targetW, dt, 36.0);
          ctrl.posW = rw.current;
          ctrl.vw = rw.velocity;
          if (Math.abs(ctrl.posX - targetX) > 0.08 || Math.abs(ctrl.posW - targetW) > 0.08) moving = true;
        }
      }
      if (ctrl.pressProgress > 0.002) {
        const rPress = springStepCritical(ctrl.pressProgress, ctrl.vPress, 0.0, dt, 18.0);
        ctrl.pressProgress = Math.max(0.0, Math.min(1.0, rPress.current));
        ctrl.vPress = rPress.velocity;
        moving = true;
      } else {
        ctrl.pressProgress = 0.0;
        ctrl.vPress = 0;
      }
    }

    // 3. Track Container Spring (Underdamped elastic rubber band on edges)
    const rTrack = springStepUnderdamped(ctrl.trackOffset, ctrl.vTrack, ctrl.targetTrackOffset, dt, 18.0, 0.55);
    ctrl.trackOffset = rTrack.current;
    ctrl.vTrack = rTrack.velocity;
    if (Math.abs(ctrl.trackOffset - ctrl.targetTrackOffset) > 0.04 || Math.abs(ctrl.vTrack) > 0.1) moving = true;
    else { ctrl.trackOffset = ctrl.targetTrackOffset; ctrl.vTrack = 0; }

    if (Math.abs(ctrl.trackOffset) > 0.04) {
      bar.style.transform = `translateX(${ctrl.trackOffset.toFixed(2)}px)`;
    } else if (bar.style.transform) {
      bar.style.transform = '';
    }

    draw(ctrl);

    if (moving || ctrl.isDragging || (ctrl.transit && ctrl.transit.active) || ctrl.pressProgress > 0.005 || Math.abs(ctrl.trackOffset) > 0.04) {
      ctrl.raf = requestAnimationFrame(t => loop(ctrl, t));
    } else {
      ctrl.prev = 0;
    }
  }

  function wake(ctrl) {
    if (!ctrl.raf) {
      ctrl.prev = 0;
      ctrl.raf = requestAnimationFrame(t => loop(ctrl, t));
    }
  }

  function selectTab(ctrl, btn, triggerClick = false) {
    if (!btn) return;
    const bar = ctrl.bar;
    const buttons = buttonsOf(bar);
    if (!buttons.includes(btn)) return;

    hideDomButtons(bar);

    // If clicking tab that is already active and settled, skip
    const barRect = bar.getBoundingClientRect();
    const r = btn.getBoundingClientRect();
    const targetX = r.left - barRect.left + 1;
    if (ctrl.activeBtn === btn && !ctrl.isDragging && (!ctrl.transit || !ctrl.transit.active) && Math.abs((ctrl.posX || 0) - targetX) < 1.0) {
      return;
    }

    startTransit(ctrl, btn);

    if (triggerClick) {
      ctrl.suppressClick = true;
      try { btn.click(); } finally { ctrl.suppressClick = false; }
    }
  }

  function syncActiveTab(ctrl, instant = false) {
    const bar = ctrl.bar;
    hideDomButtons(bar);
    const b = activeBtnOf(bar);
    if (!b) return;

    const barRect = bar.getBoundingClientRect();
    const r = b.getBoundingClientRect();
    const targetX = r.left - barRect.left + 1;
    const targetW = r.width - 2;

    // If transit is actively playing, never abort or snap it!
    if (ctrl.transit && ctrl.transit.active) {
      ctrl.transit.toX = targetX;
      ctrl.transit.toW = targetW;
      return;
    }

    if (instant || ctrl.posX === undefined) {
      ctrl.activeBtn = b;
      ctrl.candidateBtn = b;
      ctrl.targetBtn = b;
      ctrl.posX = targetX;
      ctrl.posW = targetW;
      ctrl.vx = 0; ctrl.vw = 0;
      ctrl.pressProgress = 0; ctrl.vPress = 0;
      ctrl.targetPress = 0;
      if (ctrl.transit) ctrl.transit.active = false;
      ctrl.isTransit = false;
      ctrl.trackOffset = 0; ctrl.vTrack = 0; ctrl.targetTrackOffset = 0;
      bar.style.transform = '';
      draw(ctrl);
    } else {
      if (Math.abs(ctrl.posX - targetX) < 1.0 && ctrl.pressProgress === 0) {
        ctrl.activeBtn = b;
        ctrl.targetBtn = b;
        return;
      }
      startTransit(ctrl, b);
    }
  }

  function setupBar(bar) {
    if (controllers.has(bar)) return;

    // Ensure proper positioning and transparency
    const cs = getComputedStyle(bar);
    if (cs.position === 'static') bar.style.position = 'relative';
    bar.style.overflow = 'visible';
    bar.style.setProperty('background', 'transparent', 'important');
    bar.style.setProperty('border-color', 'transparent', 'important');
    bar.style.setProperty('box-shadow', 'none', 'important');

    // Canvas overlay (inset -26px)
    const canvas = document.createElement('canvas');
    canvas.className = 'liquid-tabbar-canvas';
    bar.insertBefore(canvas, bar.firstChild);

    const built = makeGL(canvas);
    if (!built) { canvas.remove(); return; }

    const ctrl = {
      bar,
      canvas,
      ...built,
      posX: undefined,
      posW: 60,
      targetX: undefined,
      targetW: undefined,
      vx: 0, vw: 0,
      pressProgress: 0,
      vPress: 0,
      targetPress: 0,
      isPressed: false,
      isTransit: false,
      transit: null,
      trackOffset: 0,
      vTrack: 0,
      targetTrackOffset: 0,
      isDragging: false,
      justDragged: false,
      suppressClick: false,
      activeBtn: null,
      candidateBtn: null,
      targetBtn: null,
      dragCurrX: 0,
      pointerId: null,
      raf: 0,
      prev: 0
    };

    controllers.set(bar, ctrl);
    bar.__liquidTabCtrl = ctrl;
    bar.classList.add('has-liquid-tabbar');
    hideDomButtons(bar);

    syncActiveTab(ctrl, true);

    let dragStartX = 0, dragStartY = 0, dragInitialX = 0;
    let didDrag = false;

    // --- Pointer Drag / Gesture Handler ---
    bar.addEventListener('pointerdown', e => {
      if (e.button !== 0 || !e.isPrimary) return;
      const buttons = buttonsOf(bar);
      if (!buttons.length) return;

      let b = e.target.closest('button');
      if (!b || !buttons.includes(b)) {
        const barRect = bar.getBoundingClientRect();
        const clickX = e.clientX - barRect.left;
        let bestDist = Infinity;
        for (const btn of buttons) {
          const br = btn.getBoundingClientRect();
          const bc = (br.left - barRect.left) + br.width / 2;
          const dist = Math.abs(clickX - bc);
          if (dist < bestDist) { bestDist = dist; b = btn; }
        }
      }
      if (!b) return;

      if (ctrl.transit) ctrl.transit.active = false;
      ctrl.isTransit = false;

      ctrl.pointerId = e.pointerId;
      dragStartX = e.clientX;
      dragStartY = e.clientY;
      const bLeft = b.getBoundingClientRect().left - bar.getBoundingClientRect().left + 1;
      const isOverPill = ctrl.posX !== undefined && Math.abs(bLeft - ctrl.posX) < ctrl.posW * 0.6;
      dragInitialX = isOverPill ? ctrl.posX : bLeft;
      didDrag = false;
      ctrl.dragCurrX = dragInitialX;
      ctrl.targetBtn = b;
      ctrl.candidateBtn = b;
      ctrl.isPressed = true;
      ctrl.targetPress = 1.0;
      wake(ctrl);
    });

    bar.addEventListener('pointermove', e => {
      if (ctrl.pointerId !== e.pointerId) return;

      const dx = e.clientX - dragStartX;
      const dy = e.clientY - dragStartY;

      if (!ctrl.isDragging && (Math.abs(dx) > 4 || Math.abs(dy) > 4)) {
        ctrl.isDragging = true;
        didDrag = true;
        ctrl.isPressed = true;
        ctrl.targetPress = 1.0;
        try { bar.setPointerCapture(e.pointerId); } catch (_) {}
      }

      if (ctrl.isDragging) {
        const buttons = buttonsOf(bar);
        const barRect = bar.getBoundingClientRect();
        const firstBtn = buttons[0].getBoundingClientRect();
        const lastBtn = buttons[buttons.length - 1].getBoundingClientRect();

        const minX = firstBtn.left - barRect.left + 1;
        const maxX = lastBtn.left - barRect.left + 1;

        let curX = dragInitialX + dx;

        // RUBBER BAND DAMPING & ELASTIC TRACK PULL:
        // When pulling past boundary, the slider stretches with rubber band resistance,
        // and the ENTIRE track container is pulled along by up to 7px with elastic spring!
        if (curX < minX) {
          const over = minX - curX;
          const dampedOver = (over * 30) / (over + 30);
          ctrl.dragCurrX = minX - dampedOver;
          ctrl.targetTrackOffset = -Math.min(7, dampedOver * 0.35);
        } else if (curX > maxX) {
          const over = curX - maxX;
          const dampedOver = (over * 30) / (over + 30);
          ctrl.dragCurrX = maxX + dampedOver;
          ctrl.targetTrackOffset = Math.min(7, dampedOver * 0.35);
        } else {
          ctrl.dragCurrX = curX;
          const span = maxX - minX;
          const norm = span > 0 ? (curX - minX) / span * 2 - 1 : 0;
          ctrl.targetTrackOffset = 2.2 * Math.sign(norm) * Math.pow(Math.abs(norm), 2);
        }

        // Find candidate button under current pill center
        const pillCenter = ctrl.dragCurrX + ctrl.posW / 2;
        let bestBtn = buttons[0], bestDist = Infinity;
        for (const btn of buttons) {
          const br = btn.getBoundingClientRect();
          const bc = (br.left - barRect.left) + br.width / 2;
          const dist = Math.abs(pillCenter - bc);
          if (dist < bestDist) {
            bestDist = dist;
            bestBtn = btn;
          }
        }
        ctrl.candidateBtn = bestBtn;
        wake(ctrl);
      }
    });

    const handlePointerUp = e => {
      if (ctrl.pointerId !== e.pointerId) return;

      if (bar.hasPointerCapture(e.pointerId)) {
        try { bar.releasePointerCapture(e.pointerId); } catch (_) {}
      }
      ctrl.pointerId = null;
      ctrl.isPressed = false;
      ctrl.targetTrackOffset = 0; // Release spring on track container!

      if (ctrl.isDragging && didDrag) {
        ctrl.isDragging = false;
        ctrl.justDragged = true;
        setTimeout(() => { ctrl.justDragged = false; }, 80);

        if (ctrl.candidateBtn) {
          selectTab(ctrl, ctrl.candidateBtn, true);
        }
      } else {
        // Tapping / simple click -> Smooth fluid transit to target tab!
        if (ctrl.targetBtn) {
          selectTab(ctrl, ctrl.targetBtn, true);
        }
      }

      ctrl.isDragging = false;
      wake(ctrl);
    };

    bar.addEventListener('pointerup', handlePointerUp);
    bar.addEventListener('pointercancel', handlePointerUp);

    // Native click fallback: ensure click triggers smooth fluid transit
    bar.addEventListener('click', e => {
      if (ctrl.suppressClick || ctrl.justDragged) {
        return;
      }
      const b = e.target.closest('button');
      if (b && buttonsOf(bar).includes(b)) {
        selectTab(ctrl, b, false);
      }
    });

    // Watch mutations (active class changes from app code)
    const mo = new MutationObserver(records => {
      if (ctrl.suppressMutation) return;
      hideDomButtons(bar);
      if (records.some(r => r.target !== canvas)) {
        syncActiveTab(ctrl, false);
      }
    });
    mo.observe(bar, { subtree: true, childList: true, attributes: true, attributeFilter: ['class', 'aria-selected', 'hidden'] });

    // Watch resize
    const ro = new ResizeObserver(() => {
      if (ctrl.transit && ctrl.transit.active) return;
      syncActiveTab(ctrl, true);
    });
    ro.observe(bar);
  }

  function scan() {
    document.querySelectorAll(BAR_SELECTOR).forEach(setupBar);
  }

  function init() {
    scan();
    window.addEventListener('resize', () => {
      for (const ctrl of controllers.values()) syncActiveTab(ctrl, true);
    });
    const mo = new MutationObserver(scan);
    mo.observe(document.body, { subtree: true, childList: true });
  }

  window.MUILiquidTabBar = {
    init,
    scan,
    controllers,
    sync: bar => {
      const c = controllers.get(bar);
      if (c) syncActiveTab(c, false);
    }
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
