package vfs

// FilesystemDriver is the contract every filesystem backend must satisfy.
type FilesystemDriver interface {
	Write(path string, content []byte) error
	WriteString(path string, content string) error
	WriteLazy(path string, provider func() ([]byte, error)) error
	Read(path string) ([]byte, error)
	ReadString(path string) (string, error)
	Exists(path string) bool
	Remove(path string) error
	IsDir(path string) bool
	Listdir(path string) ([]string, error)
	Find(root, pattern string) ([]string, error)
	Stat(path string) (FileInfo, error)
	Clone() FilesystemDriver
}

// BuiltinFilesystemDriver wraps a *VirtualFS to implement FilesystemDriver.
type BuiltinFilesystemDriver struct {
	FS *VirtualFS
}

// NewBuiltinFilesystemDriver constructs a BuiltinFilesystemDriver backed by
// a fresh, empty VirtualFS.
func NewBuiltinFilesystemDriver() *BuiltinFilesystemDriver {
	return &BuiltinFilesystemDriver{FS: New(nil)}
}

func (b *BuiltinFilesystemDriver) Write(path string, content []byte) error {
	return b.FS.Write(path, content)
}

func (b *BuiltinFilesystemDriver) WriteString(path string, content string) error {
	return b.FS.WriteString(path, content)
}

func (b *BuiltinFilesystemDriver) WriteLazy(path string, provider func() ([]byte, error)) error {
	return b.FS.WriteLazy(path, provider)
}

func (b *BuiltinFilesystemDriver) Read(path string) ([]byte, error) {
	return b.FS.Read(path)
}

func (b *BuiltinFilesystemDriver) ReadString(path string) (string, error) {
	return b.FS.ReadString(path)
}

func (b *BuiltinFilesystemDriver) Exists(path string) bool {
	return b.FS.Exists(path)
}

func (b *BuiltinFilesystemDriver) Remove(path string) error {
	return b.FS.Remove(path)
}

func (b *BuiltinFilesystemDriver) IsDir(path string) bool {
	return b.FS.IsDir(path)
}

func (b *BuiltinFilesystemDriver) Listdir(path string) ([]string, error) {
	return b.FS.Listdir(path)
}

func (b *BuiltinFilesystemDriver) Find(root, pattern string) ([]string, error) {
	return b.FS.Find(root, pattern)
}

func (b *BuiltinFilesystemDriver) Stat(path string) (FileInfo, error) {
	return b.FS.Stat(path)
}

// Clone returns a new BuiltinFilesystemDriver backed by a clone of the
// underlying VFS, satisfying the FilesystemDriver interface.
func (b *BuiltinFilesystemDriver) Clone() FilesystemDriver {
	return &BuiltinFilesystemDriver{FS: b.FS.Clone()}
}
