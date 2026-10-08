package runtime

// Schema 3 did not store the stopped-loop policy or the detached voice's mute.
// Keep its field order separate so previously written states remain readable.
type bgmVoiceStateV3 struct {
	MediaType  string
	Source     []byte
	PositionNS int64
	Volume     uint8
	Pan        int8
}

type mediaStateV3 struct {
	Limits            MediaLimits
	GlobalVolume      uint8
	GlobalMute        bool
	OutputRemainder   uint64
	QueuedPCM16       []int16
	Clips             []ClipState
	AudioMixMode      bool
	BGMVoice          *bgmVoiceStateV3
	BGMVoiceSig       uint64
	BGMEndedSig       uint64
	BGMEndedElapsedNS int64
	BGMEndedValid     bool
}

func decodeLegacyMediaState(data []byte) (MediaState, error) {
	var old mediaStateV3
	if err := decodeStateValue(data, &old); err != nil {
		return MediaState{}, err
	}
	current := MediaState{
		Limits: old.Limits, GlobalVolume: old.GlobalVolume, GlobalMute: old.GlobalMute,
		OutputRemainder: old.OutputRemainder, QueuedPCM16: old.QueuedPCM16,
		Clips: old.Clips, AudioMixMode: old.AudioMixMode,
		BGMVoiceSig: old.BGMVoiceSig, BGMEndedSig: old.BGMEndedSig,
		BGMEndedElapsedNS: old.BGMEndedElapsedNS, BGMEndedValid: old.BGMEndedValid,
	}
	if old.BGMVoice != nil {
		// A serialized voice implies that this state was preserving background
		// music. Earlier states without a voice cannot identify an enabled policy.
		current.PreserveStoppedLoops = true
		current.BGMVoice = &BGMVoiceState{
			MediaType: old.BGMVoice.MediaType, Source: old.BGMVoice.Source,
			PositionNS: old.BGMVoice.PositionNS, Volume: old.BGMVoice.Volume,
			Pan: old.BGMVoice.Pan,
		}
	}
	return current, nil
}
