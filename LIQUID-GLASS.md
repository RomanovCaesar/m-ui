# Liquid Glass frontend

m-ui's panel, login page and public subscription page imitate Apple's
iOS 26 / macOS 26 Liquid Glass. The controls follow
[Liquid Glass WebGL Port](https://github.com/martin65536/liquid-glass-webgl)
(Apache-2.0, a web port of Kyant0/AndroidLiquidGlass): its spring constants,
lens refraction, chromatic dispersion, highlights and component geometry were
ported, and the result was compared against its live demo
(<https://glass.mt512.qzz.io/>). See `NOTICE`.

Everything is framework-free JavaScript/GLSL and CSS embedded in the Go
binary. No build step and no remote assets.

Sizing: wide screens use macOS-sized controls, screens up to 768 px wide use
iOS-sized ones.

## Where each piece lives

| File | What it does |
| --- | --- |
| `web/liquid-glass-gl.js/.css` | One full-viewport WebGL layer behind the page. It draws the wallpaper and the glass of surfaces that sit directly on it: the sidebar, login card, inbound action pills and sidebar trigger. |
| `web/liquid-optics.js/.css` | Registers those wallpaper surfaces and animates the sidebar selection. |
| `web/liquid-toggle.js/.css` | Switches (`.switch`, `.theme-switch`, `.ultra-check`, generated `.check`, `button.search-toggle`). |
| `web/liquid-tabbar.js/.css` | Tab bars (`.settings-tabbar`, `.mihomo-tabs`, `.inbound-status-tabs`, `.mihomo-editor-tabs`) and the macOS sidebar selection style. |
| `web/liquid-button.js/.css` | Button materials and press feedback. |
| `web/liquid-sheet.css` | Dialogs, the inbound drawer and menus. |
| `web/liquid-toolbar.css` | The sticky toolbar and its scroll-edge effect. |
| `web/liquid-glass.css`, `web/swiftui-components.js` | Design tokens, form widgets and the custom select. |

The WebGL layer paints behind the page content, so it can only refract the
wallpaper. Anything content scrolls under (the toolbar) or that floats above
content (dialogs, menus) must not be registered with it. Those use a CSS
backdrop-filter material instead.

## Controls

**Switch.** A port of `LiquidToggle.kt` and `DampedDragAnimation.kt`.
- Geometry: a 64×28 track with a 40×24 knob. Desktop uses 46×20 (compact
  36×16); narrow screens use 64×28 (compact 50×22).
- At rest the knob is an opaque white pebble.
- Pressing, dragging, tapping or pressing Space springs it to 1.5×. The white
  fades out and the knob becomes a clear lens that refracts the card colour
  and a scaled copy of the track colour, with 7-tap chromatic dispersion and
  an Ambient rim.
- Springs: value and press use spring(1, 1000); scale X/Y use spring(0.6, 250)
  and spring(0.7, 250); velocity uses spring(0.5, 300).
- Only real drag velocity squashes the knob, so taps never wobble.
- The lens stays up while the pointer is held.
- A ResizeObserver re-measures switches that were created while hidden, for
  example inside a closed menu.

**Tab bar.** A port of the `LiquidBottomTabs` indicator.
- At rest the selected tab sits under a dim capsule (black 10% light, white
  10% dark). Only the content that capsule covers is tinted with the accent
  (#0088FF / #0091FF), so the highlight follows the capsule while it slides.
- Pressing or dragging the selected tab grows the capsule by 78/56 into a clear
  lens. The lens is drawn on a canvas above the labels and refracts:
  - a raster of the labels and SF icons. Glyphs fade out towards the rim, so
    the steepest band never smears them into dark fragments;
  - the inner backdrop plate: a rest-height capsule across the bar, with its
    rim highlight and shadow. Its top and bottom edges are the two bent,
    colour-split lines seen while dragging.
- The real DOM labels stay underneath for focus, translation and screen
  readers.
- Keyboard: Left/Right/Home/End, skipping disabled tabs.

**Buttons.** `InteractiveHighlight.kt` press feedback:
- underdamped press spring(0.5, 300) and scale 1 + 4/48;
- a tanh-limited follow-and-stretch while held;
- an 8% flat white overlay plus a 15% radial glow at the finger.

Motion uses the independent CSS `scale` / `translate` properties, so existing
`transform` rules keep working.

Materials:

| Type | Look | Used for |
| --- | --- | --- |
| Tinted | #0088FF, or red #FF3B30 for destructive actions, white label | Primary and destructive buttons |
| Gray | iOS "gray" fill | Buttons on cards |
| Surface | White 30% glass | Round and floating buttons |

Destructive icon buttons keep the glass and only turn the glyph red.
Segmented-control options are deliberately not treated as buttons.

**Dialogs and the inbound drawer.**
- A flat dim scrim: #29293A at 23% light, #121212 at 56% dark.
- A glass card: blur 16 (8 dark), saturation 1.5, light brightness +0.2,
  surface #FAFAFA 60% / #121212 40%, and a 38% rim.
- Corner radius 28 px (34 px on narrow screens).
- Entrance: Apple's sheet curve `cubic-bezier(.32,.72,0,1)`, growing from 0.94.

**Menus.** macOS style:
- a denser frosted panel (85%) with 14 px corners;
- a blue rounded highlight with white text on the hovered item;
- red text for destructive items.

A menu's ancestors must not carry a `backdrop-filter`. It would become the
backdrop root and the menu could no longer blur the page.

**Toolbar.**
- The glass capsule is drawn on `.topbar::after`: a real backdrop blur.
- `.topbar::before` adds the iOS 26 scroll-edge effect, a fading blur band, so
  content softens as it slides under the toolbar.
- Neither effect sits on `.topbar` itself, for the backdrop-root reason above.

**Subscription page.** It uses the switch and button files, served from the
subscription asset prefix on both listeners. Its menus are restyled in the
template itself, because they are centred with `transform`.

## Dark mode

Every control has dark values from the reference palette. The WebGL glass
samples the wallpaper with the same dim as the wallpaper pass, so the sidebar
does not glow in dark themes.

The three pages mark dark mode differently:

| Page | Dark marker |
| --- | --- |
| Panel | `body.dark` |
| Login page | `html.dark` |
| Subscription page | `html[data-theme="dark"]` |

## Deliberate differences from the reference

- m-ui sits on white cards, not a colourful wallpaper, so the tab bar's plate
  shadow is tighter and darker than `Shadow.Default`.
- Lens rims use the Ambient highlight (bright/dark sides), because the
  reference's additive Default rim is invisible on white.
- Tab labels are faded near the lens rim to avoid glyph fragments.

## Validation

- `go test ./...`, including subscription asset availability on both
  listeners.
- `tests/liquid-toggle.html` has 18 checks. Serve it with
  `node tests/liquid-toggle.cjs` and open it with `#run`. It covers form
  semantics, drag, cancel, RTL, disabled, insert/remove, hold/drag keeping the
  lens, and hidden-then-shown sizing.
- `tests/liquid-optics.html` has 15 tab bar checks. Serve `tests/` with
  `/static/` mapped to `web/` and open it with `#run`. It covers tap settle,
  keyboard, cancel, drag commit, hold/drag lens, the labels raster,
  hidden/inserted/removed bars and spatial emphasis markers.
- Headless Chrome with `--virtual-time-budget` does not fire
  `requestAnimationFrame`, so open the tab bar fixture with `?timer-raf`
  there. Background browser tabs pause rAF as well.
- Checked by eye in light and dark: login, dashboard, inbound list, drawer,
  menus, Mihomo settings, panel settings and the subscription page.
- Physical iOS/Safari touch rendering is still unverified. Safari and Firefox
  get CSS blur fallbacks where WebGL or backdrop refraction is unavailable.

## References

- [Liquid Glass WebGL Port](https://github.com/martin65536/liquid-glass-webgl) (Apache-2.0): ported code and parameters
- [AndroidLiquidGlass](https://github.com/Kyant0/AndroidLiquidGlass)
- [Apple Landmarks](https://developer.apple.com/documentation/swiftui/landmarks-building-an-app-with-liquid-glass)
- [Awesome Liquid Glass](https://github.com/GetStream/awesome-liquid-glass)
