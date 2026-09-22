package transfer

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
)

func OpenSource(sources []Source, entry Entry) (io.ReadCloser, error) {
	if entry.Type != File {
		return nil, errors.New("目录条目没有文件内容")
	}
	for _, source := range sources {
		name := source.Name
		if name == "" {
			name = filepath.Base(source.Path)
		}
		name = filepath.ToSlash(name)
		if entry.Path != name && !hasPathPrefix(entry.Path, name) {
			continue
		}
		relative := path.Clean(entry.Path[len(name):])
		relative = path.Clean("." + "/" + relative)
		filename := source.Path
		if relative != "." {
			filename = filepath.Join(source.Path, filepath.FromSlash(relative))
		}
		info, err := os.Lstat(filename)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != entry.Size || !info.ModTime().UTC().Equal(entry.ModifiedAt) {
			return nil, errors.New("源文件在生成清单后发生变化")
		}
		return os.Open(filename)
	}
	return nil, errors.New("找不到清单条目对应的源文件")
}

func hasPathPrefix(value, prefix string) bool {
	return len(value) > len(prefix) && value[:len(prefix)] == prefix && value[len(prefix)] == '/'
}
