package jobs

import "os"

func removeDir(path string) error {
	return os.RemoveAll(path)
}
