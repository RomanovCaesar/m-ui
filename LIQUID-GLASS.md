# Liquid Glass frontend

The panel and login screen now use `web/liquid-optics.css` and
`web/liquid-optics.js`. The existing design tokens and form widgets remain in
`liquid-glass.css` and `swiftui-components.js`. No React build or remote assets
are required; Go embeds the new files and wallpaper.

## Material

- Navigation, toolbar, floating menus, editor panels and selection lenses use
  a separate backdrop layer. Text is not filtered.
- Rounded rectangle distance fields generate a bevel normal map. Refraction
  uses an index of 1.46 and SVG displacement of the live backdrop. The center
  stays flat; displacement is concentrated around the rim.
- A directional rim follows the pointer. Content cards have a quieter, more
  opaque material to preserve readability. The wallpaper is an original SVG.
- Chromium receives SVG backdrop displacement. Safari and Firefox receive a
  blur/tint/highlight fallback. This is a web approximation, not Apple's native
  compositor. Safari/Firefox rendering and physical iOS touch input have not
  been verified on devices.
- Optical maps are limited to 640 pixels on the longest side, cached up to 48
  shapes, and applied to at most 32 visible surfaces. Maps are rebuilt after
  layout settles, not on every pointer movement. Removed elements release
  observers, listeners and filters.

## Interaction

- One spring-driven lens follows navigation and segmented selection.
- Drag release activates one option; cancellation restores the current option.
- Disabled and hidden options are excluded. Arrow keys and Home/End work.
- On touch screens the active segment reserves horizontal dragging for its
  lens; other segments retain horizontal scrolling. Vertical scrolling remains
  available. Long strips scroll internally without widening the mobile page.
- Reduced motion disables spring animation. Reduced transparency, increased
  contrast and forced colors receive simpler materials.

## Validation

- `go test ./...` passed.
- `node --check web/liquid-optics.js` and `node --check
  web/swiftui-components.js` passed.
- Browser checks: login, navigation, light/dark rendering, mouse drag release,
  keyboard selection, sidebar, inbound editor and 390×844 mobile layout.
- `tests/liquid-optics.html`: 10 browser checks passed, covering hidden bars,
  spring alignment, disabled options, cancellation, single activation,
  revealing/inserting/removing controls, and bounded filters. Synthetic pointer
  checks stub native capture; real mouse dragging was checked separately.
- Accessibility media-query fallbacks are implemented but were not emulated
  in the browser verification session.

To run the fixture, serve it with `/static/` mapped to `web/`, then press
**Run checks**. The fixture does not call the panel API.

## Running the result

### Shared liquid switches

`web/liquid-toggle.css` and `web/liquid-toggle.js` now enhance switches across
login, panel settings, dynamically generated inbound/client rows, protocol
editors and public subscription settings. Standard switches use a 64×32 rail
with a 40×28 capsule; compact switches use 52×26 and 32×22. Pressing expands the
capsule and lowers its tint so the refracted track shows through. Only two SVG
lens maps are shared by all switches. Checkbox values, form serialization,
reset, disabled behavior and existing handlers remain native. Selection
checkboxes in tables/sync matrices retain their original purpose.

The public subscription listener exposes only the two additional switch assets
alongside its existing QR asset, using its configured subscription prefix.

Validation: all Go tests passed, including asset availability on both listeners;
the browser fixture (`node tests/liquid-toggle.cjs`) passed 14 interaction checks.
Actual login theme rendering and Space activation were verified. Real mouse
drag and subsequent Space activation passed on the fixture with production
panel styles. Authenticated panel visual checking was not completed in this
round: automatic approval rejected signing into the isolated test panel.
Physical iOS/Safari touch rendering remains unverified. The previous executable
is backed up at `.runtime-glass/m-ui-before-toggle.exe`.

`m-ui.exe` in this test directory has been rebuilt. Run it with this directory
as the working directory, as before. If an older instance is already running,
restart that test instance to load the embedded frontend. The same build is at
`dist/m-ui-liquid-glass.exe`; the preceding executable is backed up at
`.runtime-glass/m-ui-before-optics.exe`.

The development preview used isolated data in `.runtime-glass/data`, with no
Mihomo core running. The original sibling `m-ui` directory was not changed.

## References

- [Apple Landmarks](https://developer.apple.com/documentation/swiftui/landmarks-building-an-app-with-liquid-glass)
- [Liquid Glass React](https://github.com/rdev/liquid-glass-react)
- [AndroidLiquidGlass](https://github.com/Kyant0/AndroidLiquidGlass)
- [Liquid Glass web demo](https://github.com/archisvaze/liquid-glass)
- [Awesome Liquid Glass](https://github.com/GetStream/awesome-liquid-glass)

These informed the material/interaction direction. The new renderer is a local,
framework-independent implementation; no source code or assets from these
repositories were vendored.
