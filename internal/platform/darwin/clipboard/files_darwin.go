//go:build darwin

package darwinclipboard

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
)

const readFilesScript = `ObjC.import('AppKit');
const p=$.NSPasteboard.generalPasteboard;
const legacy=p.propertyListForType('NSFilenamesPboardType');
const paths=legacy.isNil()?[]:ObjC.deepUnwrap(legacy);
if(paths.length===0){
 const classes=$.NSMutableArray.alloc.init;
 classes.addObject($.NSURL.class);
 const xs=p.readObjectsForClassesOptions(classes,$.NSDictionary.dictionary);
 for(let i=0;i<Number(xs.count);i++){ const url=xs.objectAtIndex(i); if(url.isFileURL) paths.push(ObjC.unwrap(url.path)); }
}
JSON.stringify({revision:Number(p.changeCount),paths:paths});`
const writeFilesScript = `ObjC.import('AppKit');
function run(argv){
 const paths=JSON.parse(argv[0]);
 const p=$.NSPasteboard.generalPasteboard;
 p.declareTypesOwner(['NSFilenamesPboardType'],null);
 if(!p.setPropertyListForType(paths,'NSFilenamesPboardType')) throw Error('file pasteboard publication failed');
}`

type FileBackend struct{}

func NewFiles() *FileBackend { return &FileBackend{} }

func (b *FileBackend) ReadFiles(ctx context.Context) ([]string, uint64, bool, error) {
	data, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", readFilesScript).Output()
	if err != nil {
		return nil, 0, false, err
	}
	var result struct {
		Revision uint64   `json:"revision"`
		Paths    []string `json:"paths"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, 0, false, err
	}
	return result.Paths, result.Revision, len(result.Paths) > 0, nil
}

func (b *FileBackend) WriteFiles(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return errors.New("文件路径不能为空")
	}
	data, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	return exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", writeFilesScript, "--", string(data)).Run()
}
