package quirkdb

// EADSRuntimeProfile binds an observed OEM ABI to exact executable content.
// Zero dimensions preserve the existing factory geometry for older profiles.
type EADSRuntimeProfile struct {
	Key            EADSTitleRuntime
	ID             string
	Carrier        string
	Width, Height  int
	Legacy         bool
	TimerBootstrap bool
	Pointer        bool
}

var eadsProfiles = [...]EADSRuntimeProfile{
	{Key: MinigameQVGAOEM, ID: "wipi-1.2.1/skt/samsung/sch-w830/minigame-qvga-oem", Carrier: "skt"},
	{Key: MinigameQVGAOEMW860, ID: "wipi-1.2.1/skt/samsung/sch-w860/minigame-qvga-oem", Carrier: "skt"},
	{Key: MinigameWQVGA, ID: "wipi-1.2.1/skt/samsung/sch-w770/minigame-wqvga", Carrier: "skt",
		Width: 240, Height: 376, TimerBootstrap: true, Pointer: true},
	{Key: BombLink, ID: "wipi-1.0/skt/samsung/sch-w210/bomb-link", Carrier: "skt",
		Width: 240, Height: 296, Legacy: true},
}

func LookupEADSRuntime(imageName, sourceSHA256, recordSHA256 string) (EADSRuntimeProfile, bool) {
	for _, profile := range eadsProfiles {
		if profile.Key.Matches(imageName, sourceSHA256, recordSHA256) {
			return profile, true
		}
	}
	return EADSRuntimeProfile{}, false
}
