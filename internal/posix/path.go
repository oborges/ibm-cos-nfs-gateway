package posix

import (
	"path"
	"strings"
)

// PathTranslator handles translation between filesystem paths and COS object keys
type PathTranslator struct {
	prefix string
}

// NewPathTranslator creates a new path translator
func NewPathTranslator(prefix string) *PathTranslator {
	// Ensure prefix doesn't start with /
	prefix = strings.TrimPrefix(prefix, "/")
	// Ensure prefix ends with / if not empty
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	return &PathTranslator{
		prefix: prefix,
	}
}

// ToObjectKey converts a filesystem path to a COS object key
func (t *PathTranslator) ToObjectKey(fsPath string) string {
	// Clean the path
	fsPath = path.Clean(fsPath)

	// Remove leading slash
	fsPath = strings.TrimPrefix(fsPath, "/")

	// Handle root
	if fsPath == "." || fsPath == "" {
		return t.prefix
	}

	// Combine with prefix
	return t.prefix + fsPath
}

// ToFSPath converts a COS object key to a filesystem path
func (t *PathTranslator) ToFSPath(objectKey string) string {
	// Remove prefix
	if t.prefix != "" {
		objectKey = strings.TrimPrefix(objectKey, t.prefix)
	}

	// Ensure leading slash
	if !strings.HasPrefix(objectKey, "/") {
		objectKey = "/" + objectKey
	}

	// Clean the path
	return path.Clean(objectKey)
}

// IsDirectory checks if a path represents a directory
func IsDirectory(p string) bool {
	return strings.HasSuffix(p, "/")
}

// ToDirectoryKey converts a path to a directory key (with trailing /)
func ToDirectoryKey(p string) string {
	if !strings.HasSuffix(p, "/") {
		return p + "/"
	}
	return p
}

// ToFileKey converts a path to a file key (without trailing /)
func ToFileKey(p string) string {
	return strings.TrimSuffix(p, "/")
}

// GetParentPath returns the parent directory path
func GetParentPath(p string) string {
	if p == "/" || p == "" {
		return "/"
	}

	// Remove trailing slash
	p = strings.TrimSuffix(p, "/")

	// Get directory
	dir := path.Dir(p)
	if dir == "." {
		return "/"
	}

	return dir
}

// GetBaseName returns the base name of a path
func GetBaseName(p string) string {
	// Remove trailing slash
	p = strings.TrimSuffix(p, "/")

	return path.Base(p)
}

// JoinPath joins path components
func JoinPath(components ...string) string {
	return path.Join(components...)
}

// SplitPath splits a path into directory and file
func SplitPath(p string) (dir, file string) {
	dir, file = path.Split(p)
	return
}

// NormalizePath normalizes a filesystem path
func NormalizePath(p string) string {
	// Clean the path
	p = path.Clean(p)

	// Ensure it starts with /
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	return p
}

// IsValidPath checks if a path is valid
func IsValidPath(p string) bool {
	// Empty path is invalid
	if p == "" {
		return false
	}

	// Path must start with /
	if !strings.HasPrefix(p, "/") {
		return false
	}

	// Check for invalid characters
	if strings.Contains(p, "//") {
		return false
	}

	// Check for null bytes
	if strings.Contains(p, "\x00") {
		return false
	}

	return true
}

// GetDepth returns the depth of a path
func GetDepth(p string) int {
	if p == "/" {
		return 0
	}

	p = strings.Trim(p, "/")
	if p == "" {
		return 0
	}

	return strings.Count(p, "/") + 1
}

// IsDescendant checks if child is a descendant of parent
func IsDescendant(parent, child string) bool {
	parent = NormalizePath(parent)
	child = NormalizePath(child)

	// Ensure parent ends with /
	if !strings.HasSuffix(parent, "/") {
		parent += "/"
	}

	return strings.HasPrefix(child, parent)
}

// ListPrefix returns the prefix for listing objects under a directory
func ListPrefix(dirPath string) string {
	dirPath = NormalizePath(dirPath)

	// Remove leading slash
	dirPath = strings.TrimPrefix(dirPath, "/")

	// Ensure trailing slash for directory
	if dirPath != "" && !strings.HasSuffix(dirPath, "/") {
		dirPath += "/"
	}

	return dirPath
}

// Made with Bob
