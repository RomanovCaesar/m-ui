This package is adapted from the bundled MetaCubeX/mihomo common/convert
source (mihomo-1.19.31). It is licensed under GPL-3.0. The license text is
included both next to this package and in the repository root LICENSE file.

Local changes remove core logging (which included credentials) and random
User-Agent generation, use the existing multi-alphabet Base64 decoder for SS,
and omit the core-dependent SS cipher probe. Import validation and small URI
compatibility fixes live in m-ui's outbound import adapter.
