# Bundled ICC profiles

Embedded by `fixups_colour.go` into any OutputIntent, `Default*` colour space
or repaired ICCBased space this package injects, so these bytes end up inside
converted files. PDF/A-1 permits ICC v2.x only, and an OutputIntent profile
must be device class `prtr` or `mntr` (`verify.ValidateICCProfileStream`), which
is what rules out most of what is published.

| File | Colour space | Source |
| --- | --- | --- |
| `sRGB2014.icc` | RGB | the ICC's own sRGB v2 profile |
| `Small-footprint_FOGRA39v2.icc` | CMYK | FOGRA39, size-reduced and converted to v2 |
| `sGrey-v2-micro.icc` | Gray | Compact-ICC-Profiles |

Licences and copyright holders are in the repository's `NOTICE`.
