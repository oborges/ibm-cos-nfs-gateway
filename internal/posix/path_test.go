package posix

import (
	"testing"
)

func TestPathTranslator_ToObjectKey(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		fsPath   string
		expected string
	}{
		{
			name:     "root path with no prefix",
			prefix:   "",
			fsPath:   "/",
			expected: "",
		},
		{
			name:     "simple file with no prefix",
			prefix:   "",
			fsPath:   "/file.txt",
			expected: "file.txt",
		},
		{
			name:     "nested file with no prefix",
			prefix:   "",
			fsPath:   "/dir/subdir/file.txt",
			expected: "dir/subdir/file.txt",
		},
		{
			name:     "file with prefix",
			prefix:   "myprefix",
			fsPath:   "/file.txt",
			expected: "myprefix/file.txt",
		},
		{
			name:     "nested file with prefix",
			prefix:   "myprefix/",
			fsPath:   "/dir/file.txt",
			expected: "myprefix/dir/file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewPathTranslator(tt.prefix)
			result := translator.ToObjectKey(tt.fsPath)
			if result != tt.expected {
				t.Errorf("ToObjectKey() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsDirectory(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/dir/", true},
		{"/file.txt", false},
		{"dir/subdir/", true},
		{"file", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := IsDirectory(tt.path)
			if result != tt.expected {
				t.Errorf("IsDirectory(%s) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestGetParentPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/", "/"},
		{"/file.txt", "/"},
		{"/dir/file.txt", "/dir"},
		{"/dir/subdir/file.txt", "/dir/subdir"},
		{"/dir/", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := GetParentPath(tt.path)
			if result != tt.expected {
				t.Errorf("GetParentPath(%s) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestGetBaseName(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/file.txt", "file.txt"},
		{"/dir/file.txt", "file.txt"},
		{"/dir/subdir/", "subdir"},
		{"/", "."},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := GetBaseName(tt.path)
			if result != tt.expected {
				t.Errorf("GetBaseName(%s) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"file.txt", "/file.txt"},
		{"/file.txt", "/file.txt"},
		{"/dir/../file.txt", "/file.txt"},
		{"/dir/./file.txt", "/dir/file.txt"},
		{"//dir//file.txt", "/dir/file.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := NormalizePath(tt.path)
			if result != tt.expected {
				t.Errorf("NormalizePath(%s) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestIsValidPath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/file.txt", true},
		{"/dir/file.txt", true},
		{"", false},
		{"file.txt", false},
		{"/dir//file.txt", false},
		{"/dir/\x00file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := IsValidPath(tt.path)
			if result != tt.expected {
				t.Errorf("IsValidPath(%s) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestGetDepth(t *testing.T) {
	tests := []struct {
		path     string
		expected int
	}{
		{"/", 0},
		{"/file.txt", 1},
		{"/dir/file.txt", 2},
		{"/dir/subdir/file.txt", 3},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := GetDepth(tt.path)
			if result != tt.expected {
				t.Errorf("GetDepth(%s) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestIsDescendant(t *testing.T) {
	tests := []struct {
		parent   string
		child    string
		expected bool
	}{
		{"/", "/file.txt", true},
		{"/dir", "/dir/file.txt", true},
		{"/dir", "/dir/subdir/file.txt", true},
		{"/dir", "/other/file.txt", false},
		{"/dir/subdir", "/dir/file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.parent+"_"+tt.child, func(t *testing.T) {
			result := IsDescendant(tt.parent, tt.child)
			if result != tt.expected {
				t.Errorf("IsDescendant(%s, %s) = %v, want %v", tt.parent, tt.child, result, tt.expected)
			}
		})
	}
}

// Made with Bob
