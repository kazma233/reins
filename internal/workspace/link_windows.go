//go:build windows

// Windows 平台：junction（mount point reparse point）的读取、创建与删除，
// 对齐 Rust junction crate 的行为。软链接分支在 targets.go 的平台无关路径
// 里处理；这里只管 reparse point。
package workspace

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"reins/internal/support"
)

const (
	// FILE_ATTRIBUTE_REPARSE_POINT：reparse point 的文件属性位。
	fileAttributeReparsePoint uint32 = 0x400

	// IO_REPARSE_TAG_MOUNT_POINT：junction 的 reparse tag。
	ioReparseTagMountPoint uint32 = windows.IO_REPARSE_TAG_MOUNT_POINT

	// reparse 数据缓冲区头之后的 mount point 结构布局（字节数）：
	// SubstituteNameOffset/Length + PrintNameOffset/Length，共 8 字节。
	mountPointHeaderSize = 8
)

// readPlatformDirectoryLinkTarget：目录带 reparse point 属性时读 junction
// 目标；普通目录返回 nil。
func readPlatformDirectoryLinkTarget(path string, metadata os.FileInfo) (*string, error) {
	attributes := windowsFileAttributes(metadata)
	if attributes&fileAttributeReparsePoint == 0 {
		return nil, nil
	}
	return readWindowsJunctionTarget(path)
}

// readPlatformBrokenLinkTarget：断链 junction 的 Lstat 会报 NotFound，但
// reparse 数据还在，直接读可以保留原目标路径。
func readPlatformBrokenLinkTarget(path string) (*string, error) {
	return readWindowsJunctionTarget(path)
}

// readWindowsJunctionTarget：读 junction 的目标路径（canonicalize，失败回落
// 原值）。不是 junction、或路径不存在时返回 nil；其他读取错误上报。
func readWindowsJunctionTarget(path string) (*string, error) {
	tag, data, err := readReparsePoint(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if errors.Is(err, errNotReparsePoint) {
			return nil, nil
		}
		return nil, fmt.Errorf("Failed to read junction target %s: %w", path, err)
	}
	if tag != ioReparseTagMountPoint {
		// 不是 mount point tag（符号链接走软链接分支；其他 tag 不属于目录
		// 链接的职责范围）。
		return nil, nil
	}

	target := mountPointSubstituteName(data)
	if target == "" {
		return nil, nil
	}
	if canonical, err := support.Canonicalize(target); err == nil {
		return &canonical, nil
	}
	return &target, nil
}

// errNotReparsePoint：目标不是 reparse point（junction 读取按无链接处理）。
var errNotReparsePoint = errors.New("not a reparse tag mount point")

// readReparsePoint：打开路径（不解析链接本身）并取 reparse 数据缓冲区。
// 返回 tag 与 tag 之后的 payload。
func readReparsePoint(path string) (uint32, []byte, error) {
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, nil, err
	}
	handle, err := windows.CreateFile(utf16Path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, nil, err
	}
	defer windows.CloseHandle(handle)

	buffer := make([]byte, windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var returned uint32
	if err := windows.DeviceIoControl(handle, windows.FSCTL_GET_REPARSE_POINT, nil, 0, &buffer[0], uint32(len(buffer)), &returned, nil); err != nil {
		return 0, nil, err
	}
	if len(buffer) < 8 {
		return 0, nil, errNotReparsePoint
	}
	tag := binary.LittleEndian.Uint32(buffer[0:4])
	// REPARSE_DATA_BUFFER 布局：ReparseTag u32、ReparseDataLength u16、
	// Reserved u16，之后是各 tag 自己的 payload。
	payloadLength := int(binary.LittleEndian.Uint16(buffer[4:6]))
	payload := buffer[8 : 8+payloadLength]
	return tag, payload, nil
}

// mountPointSubstituteName：从 mount point payload 取 substitute name
// （\??\C:\path 形态）并剥掉 \??\ 前缀；空 substitute name 时退回 print name。
func mountPointSubstituteName(payload []byte) string {
	if len(payload) < mountPointHeaderSize {
		return ""
	}
	subOffset := int(binary.LittleEndian.Uint16(payload[0:2]))
	subLength := int(binary.LittleEndian.Uint16(payload[2:4]))
	printOffset := int(binary.LittleEndian.Uint16(payload[4:6]))
	printLength := int(binary.LittleEndian.Uint16(payload[6:8]))

	substitute := utf16Substring(payload, mountPointHeaderSize, subOffset, subLength)
	if substitute != "" {
		return strings.TrimPrefix(substitute, `\??\`)
	}
	return utf16Substring(payload, mountPointHeaderSize, printOffset, printLength)
}

// utf16Substring：按 u16 偏移/长度从 PathBuffer 取字符串。
func utf16Substring(payload []byte, base int, offset int, length int) string {
	start := base + offset
	end := start + length
	if start < 0 || end > len(payload) || start > end {
		return ""
	}
	chars := make([]uint16, 0, length/2+1)
	for i := start; i+1 < end; i += 2 {
		chars = append(chars, binary.LittleEndian.Uint16(payload[i:i+2]))
	}
	return windows.UTF16ToString(chars)
}

// createJunction：在 destination 建 junction 指向 source。destination 不存
// 在时由 OPEN_ALWAYS + BACKUP_SEMANTICS 建目录（junction crate 同款行为）。
func createJunction(source string, destination string) error {
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	utf16Destination, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(utf16Destination, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)

	buffer := buildMountPointReparseData(absoluteSource)
	var returned uint32
	if err := windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil); err != nil {
		return err
	}
	return nil
}

// deleteJunction：摘掉 reparse 数据（保留空目录本体；目录删除由调用方做）。
func deleteJunction(path string) error {
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(utf16Path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)

	// FSCTL_DELETE_REPARSE_POINT 入参：tag u32 + Reserved u32。
	input := make([]byte, 8)
	binary.LittleEndian.PutUint32(input[0:4], ioReparseTagMountPoint)
	var returned uint32
	if err := windows.DeviceIoControl(handle, windows.FSCTL_DELETE_REPARSE_POINT, &input[0], uint32(len(input)), nil, 0, &returned, nil); err != nil {
		return err
	}
	return nil
}

// buildMountPointReparseData：构造 FSCTL_SET_REPARSE_POINT 的输入缓冲区
// （REPARSE_DATA_BUFFER + mount point payload）。substitute name 带 \??\
// 前缀，print name 用可读拼写——与 junction crate 落盘的字节布局一致。
func buildMountPointReparseData(target string) []byte {
	substitute := utf16Encode(`\??\` + target)
	printName := utf16Encode(target)
	pathBufferBytes := len(substitute) + len(printName)

	// 8 字节 header + 8 字节 mount point 字段 + path buffer。
	payloadLength := mountPointHeaderSize + pathBufferBytes
	buffer := make([]byte, 8+payloadLength)
	binary.LittleEndian.PutUint32(buffer[0:4], ioReparseTagMountPoint)
	binary.LittleEndian.PutUint16(buffer[4:6], uint16(payloadLength))

	offset := 8
	binary.LittleEndian.PutUint16(buffer[offset:offset+2], 0)
	binary.LittleEndian.PutUint16(buffer[offset+2:offset+4], uint16(len(substitute)))
	binary.LittleEndian.PutUint16(buffer[offset+4:offset+6], uint16(len(substitute)))
	binary.LittleEndian.PutUint16(buffer[offset+6:offset+8], uint16(len(printName)))
	copy(buffer[offset+mountPointHeaderSize:], substitute)
	copy(buffer[offset+mountPointHeaderSize+len(substitute):], printName)
	return buffer
}

func utf16Encode(value string) []byte {
	encoded, err := syscall.UTF16FromString(value)
	if err != nil {
		return nil
	}
	// UTF16FromString 带结束符；PathBuffer 里以成对 u16 计长，不含结束符。
	if len(encoded) > 0 && encoded[len(encoded)-1] == 0 {
		encoded = encoded[:len(encoded)-1]
	}
	if len(encoded) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&encoded[0])), len(encoded)*2)
}

// removePlatformDirectoryLink：junction 先摘 reparse 数据再删目录本体；
// 不是 junction 返回 false（交给通用路径处理）。
func removePlatformDirectoryLink(path string) (bool, error) {
	linked, err := readDirectoryLinkTarget(path)
	if err != nil {
		return false, err
	}
	if linked == nil {
		return false, nil
	}
	if err := deleteJunction(path); err != nil {
		return false, fmt.Errorf("Failed to delete junction %s: %w", path, err)
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("Failed to remove junction %s: %w", path, err)
	}
	return true, nil
}

// createDirectorySymlink：Windows 的目标目录链接即 junction（无需开发者
// 模式/管理员权限的目录链接形态）。
func createDirectorySymlink(source string, destination string) error {
	if err := createJunction(source, destination); err != nil {
		return fmt.Errorf("Failed to create junction %s: %w", destination, err)
	}
	return nil
}

// windowsFileAttributes：从 Lstat 的平台数据取文件属性位。
func windowsFileAttributes(metadata os.FileInfo) uint32 {
	if sys, ok := metadata.Sys().(*syscall.Win32FileAttributeData); ok {
		return sys.FileAttributes
	}
	return 0
}
