package doctor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/model"
)

func Filesystems(paths config.Paths) error {
	for _, kind := range []model.Kind{model.KindTV, model.KindMovie, model.KindAnime} {
		source, target := paths.Roots(kind)
		info, err := os.Stat(source)
		if err != nil {
			return fmt.Errorf("%s source root: %w", kind, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s source root is not a directory", kind)
		}
		if err := os.MkdirAll(target, 0755); err != nil {
			return fmt.Errorf("%s target root: %w", kind, err)
		}
		f, err := os.CreateTemp(source, ".plexlink-doctor-*")
		if err != nil {
			return fmt.Errorf("%s create hardlink probe: %w", kind, err)
		}
		src := f.Name()
		f.Close()
		dst := filepath.Join(target, filepath.Base(src))
		linkErr := os.Link(src, dst)
		removeDstErr := error(nil)
		if linkErr == nil {
			removeDstErr = os.Remove(dst)
		}
		removeSrcErr := os.Remove(src)
		if linkErr != nil {
			return fmt.Errorf("%s source/target hardlink probe: %w", kind, linkErr)
		}
		if removeDstErr != nil || removeSrcErr != nil {
			return fmt.Errorf("%s clean hardlink probe: target=%v source=%v", kind, removeDstErr, removeSrcErr)
		}
	}
	return nil
}
