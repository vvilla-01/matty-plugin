//go:build villanet_standin

package appcheck

// developmentBuild is true only in the development build (the
// villanet_standin tag, which the plugin's own guard keeps out of every
// release): only there can a Policy's Development allowances be taken.
const developmentBuild = true
