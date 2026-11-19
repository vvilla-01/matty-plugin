package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// The plugin's own directory, beside Mattermost's other files:
//
//	<FileSettings.Directory>/ceo.villa.matty/
//
// Clips keep their recordings, drafts and published clips in it
// (clips_files.go), the title lookups keep the pictures they relay
// (media_disk.go), and people's profile banners are kept there (banners.go).
// Each keeps to a subdirectory of its own and never reaches into another's:
// Clips wipe ring/ and drafts/ by name, the pictures live in media/, and the
// banners in banners/.
//
// Worked out here, once, for all three, so that none depends on another being
// switched on. Only the local file driver will do: the plugin API can write a
// file into Mattermost's file store but never delete one, so nothing kept
// through it could ever be freed, and the only directory there is to write to
// directly is a local one.

var (
	errDataRootNoSettings = errors.New("mattermost's file storage settings could not be read")
	errDataRootNotLocal   = errors.New("mattermost's File Storage System is not Local file system")
)

// dataRootError is a directory that is set but could not be made absolute,
// which only happens when the working directory cannot be found. It keeps the
// directory as configured, for whoever tells the admin.
type dataRootError struct {
	directory string
	err       error
}

func (e *dataRootError) Error() string {
	return fmt.Sprintf("%s: %v", e.directory, e.err)
}

func (e *dataRootError) Unwrap() error {
	return e.err
}

// resolveDataRoot is the plugin's own directory, from Mattermost's own file
// settings, or why there is none. GetConfig rather than the unsanitised
// config: sanitising blanks salts and storage secrets, never the driver or the
// directory. The directory may be relative to Mattermost's working directory,
// which this process inherits, so it is made absolute here: nothing is ever
// built from a relative or an empty root, which would be wherever Mattermost
// happens to be running.
func (p *Plugin) resolveDataRoot() (string, error) {
	cfg := p.API.GetConfig()
	if cfg == nil || cfg.FileSettings.DriverName == nil || cfg.FileSettings.Directory == nil {
		return "", errDataRootNoSettings
	}
	if *cfg.FileSettings.DriverName != model.ImageDriverLocal {
		return "", errDataRootNotLocal
	}

	directory := strings.TrimSpace(*cfg.FileSettings.Directory)
	if directory == "" {
		return "", errDataRootNoSettings
	}

	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", &dataRootError{directory: directory, err: err}
	}

	return filepath.Join(absolute, manifestID), nil
}
