package application

import "github.com/mirusu400/aram-core/application/internal/minigame"

// EADSEventResult and EADSFrameStats keep their public identity on the
// application package while the minigame runtime lives in an internal
// subpackage.
type EADSEventResult = minigame.EADSEventResult

type EADSFrameStats = minigame.EADSFrameStats
