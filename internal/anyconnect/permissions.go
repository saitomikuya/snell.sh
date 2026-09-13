package anyconnect

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/proxy-panel/proxy-panel/internal/config"
)

// repairPersistentOwnership keeps files written by the privileged agent
// readable by the unprivileged panel server. In Docker, PANEL_UID/PANEL_GID
// identify the account used by panel-web; the defaults match the image.
func repairPersistentOwnership(root string) error {
	return config.RepairOwnership(root, panelID("PANEL_UID", 10001), panelID("PANEL_GID", 10001))
}

func panelID(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err == nil && value > 0 {
		return value
	}
	return fallback
}

func repairPersistentTrees(roots ...string) error {
	for _, root := range roots {
		if err := repairPersistentOwnership(filepath.Clean(root)); err != nil {
			return err
		}
	}
	return nil
}
