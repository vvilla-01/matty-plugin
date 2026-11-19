//go:build !villanet_standin

package appcheck

// developmentBuild is false in every build that is released, and the
// compiler leaves out every allowance it guards: a software security level,
// a root that is not Google's, a boot state a real phone is refused for.
// development_standin.go is the development build's twin.
const developmentBuild = false
