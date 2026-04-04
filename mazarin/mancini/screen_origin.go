package mancini

// screenOriginX and screenOriginY are the screen-space coordinates of
// the app content area's (0,0) point. Set once per shepherd from
// BackingStoreReady.AppX / AppY.
//
// These are used by Interactor.ScreenCoordConvertTo/From to translate
// between interactor-local and screen-absolute coordinates.
var screenOriginX, screenOriginY int64

// SetScreenOrigin records the screen-space position of the app content
// origin. Call this once from BackingStoreReady with AppX, AppY (NOT
// AppX+LeftInset — AppX already points to the content area).
func SetScreenOrigin(x, y int64) {
	screenOriginX = x
	screenOriginY = y
}

// ScreenOrigin returns the screen-space position of the app content origin.
func ScreenOrigin() (int64, int64) {
	return screenOriginX, screenOriginY
}
