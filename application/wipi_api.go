package application

import wipirt "github.com/mirusu400/aram-core/application/internal/wipi"

// WIPIFrameStats and WIPIAPICoverage keep their public identity on the
// application package while the WIPI runtime lives in an internal subpackage.
type WIPIFrameStats = wipirt.WIPIFrameStats

type WIPIAPICoverage = wipirt.WIPIAPICoverage
