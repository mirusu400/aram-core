package application

// BREWFrameStats reports frames presented by an authenticated BREW guest.
// FrameValid is true only after guest code changed the RGB565 surface and
// committed it through IDisplay::Update.
type BREWFrameStats struct {
	PresentCount uint64
	FrameValid   bool
}
