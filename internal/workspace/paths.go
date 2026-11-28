// paths.go：workspace 的寻址层。技能路径/MCP 路径以条目名直接寻址（散装
// 模型，无复合地址）；本文件持有落点链接的探测与归属、参与分发的 skip
// 判定。「地址怎么解析、谁参与分发」的判定不与 DTO 同居。
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reins/internal/support"
)

// ---------------------------------------------------------------------------
// 目录链接探测
// ---------------------------------------------------------------------------

// isLinkedSkillDir：路径是不是一个目录链接。
func isLinkedSkillDir(path string) (bool, error) {
	linked, err := readSkillDirLink(path)
	if err != nil {
		return false, err
	}
	return linked != nil, nil
}

func readSkillDirLink(path string) (*string, error) {
	return readDirectoryLinkTarget(path)
}

// readDirectoryLinkTarget：软链接/junction 的目标路径；不是链接返回 nil。
func readDirectoryLinkTarget(path string) (*string, error) {
	metadata, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Windows 的断链 junction 仍可能保留 reparse point，但 Lstat 会
			// 报 NotFound；直接读 reparse 数据可保留原目标路径。非 Windows
			// 平台断链没有可读数据，返回 nil。
			return readPlatformBrokenLinkTarget(path)
		}
		return nil, fmt.Errorf("读取 %s 的元数据失败：%v", path, err)
	}
	if metadata.Mode()&os.ModeSymlink != 0 {
		raw, err := os.Readlink(path)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 的软链接失败：%v", path, err)
		}
		resolved := resolveSymlinkTargetPath(path, raw)
		return &resolved, nil
	}
	return readPlatformDirectoryLinkTarget(path, metadata)
}

// resolveSymlinkTargetPath：软链接目标解析（相对链接基于链接所在目录），并
// canonicalize（失败回落原路径）。与 skill_install.go 的同名函数不同：那
// 一份不做 canonicalize（分类时保留拼写），这份用于展示与归属表键。
func resolveSymlinkTargetPath(destination string, linkTarget string) string {
	resolved := linkTarget
	if filepath.IsAbs(linkTarget) {
		resolved = linkTarget
	} else {
		resolved = joinPath(filepath.Dir(destination), linkTarget)
	}
	if canonical, err := support.Canonicalize(resolved); err == nil {
		return canonical
	}
	return resolved
}

// ---------------------------------------------------------------------------
// 寻址与 skip 判定
// ---------------------------------------------------------------------------

// SkillDirLink：落点链接的探测结果。链接另一端是受管技能路径时的条目名、
// 解出的真实目录（不是链接时 nil）。
type SkillDirLink struct {
	// LinkedPathID：链接的另一端是某条受管技能路径的目录时给出它的条目名；
	// 外部目录/断链时为空。
	LinkedPathID  string
	HasLinkedPath bool
	// LinkTarget：落点不是链接时为 nil。
	LinkTarget string
	IsLink     bool
}

// IsAlias：落点是一个目录链接（内容由链接另一端负责）。
func (l *SkillDirLink) IsAlias() bool { return l.IsLink }

// View：转成 wire 形态（可选字段只发有值的那部分）。
func (l *SkillDirLink) View() *SkillDirLinkView {
	view := &SkillDirLinkView{}
	if !l.IsLink {
		return view
	}
	if l.HasLinkedPath {
		id := l.LinkedPathID
		view.LinkedPathID = &id
	}
	view.LinkTarget = support.DisplayPath(l.LinkTarget)
	return view
}

// PathSkip：技能路径不参与分发的原因。调用方只决定表现方式（报错 / 提示 /
// 静默），「为什么不参与」由这里回答。
type PathSkip int

const (
	// SkipMissing：条目不存在：输入来自客户端（弹窗快照、命令参数）时才会
	// 出现。
	SkipMissing PathSkip = iota + 1
	// SkipLinkedDir：技能路径的目录整层是一个目录链接（指向另一条受管技能
	// 路径、外部目录或已断链）：内容由链接的另一端负责，本条目不参与安装
	// 与清理。对所有条目统一生效（决策记录 2026-11-28-skill-and-mcp-paths.md
	// 第 6 条；旧规则只对项目级 target 生效）。
	SkipLinkedDir
)

// pathUnavailable：「技能路径不可用」的唯一说法：调用方只需决定「报错还是
// 记一条 warning」，不必各自措辞。
func pathUnavailable(name string) error {
	return fmt.Errorf("技能路径不存在：%s", name)
}

// skillPathByName：唯一的技能路径寻址入口。缺失返回 nil。
func (c *ResolvedManagerConfig) skillPathByName(name string) *ResolvedSkillPath {
	return c.SkillPaths[name]
}

// mcpPathByName：唯一的 MCP 路径寻址入口。缺失返回 nil。
func (c *ResolvedManagerConfig) mcpPathByName(name string) *ResolvedMcpPath {
	return c.McpPaths[name]
}

// skipReason：该技能路径是否参与技能分发。判定只有这一处；调用方只决定
// 「跳过时怎么表现」（报错 / warning / 静默）。
//
// 为什么放在寻址层而不是安装面：它回答的是「这个条目可用吗」，而不是技能
// 安装的私有规则。
func (c *ResolvedManagerConfig) skipReason(name string) (*PathSkip, error) {
	path := c.skillPathByName(name)
	if path == nil {
		missing := SkipMissing
		return &missing, nil
	}
	// 目录整层指向别处时，内容由链接的另一端负责，这里不自作主张。
	//
	// 注意：这里只看「是不是链接」，不看「指向谁」；与展示层的
	// linkedPathID（只看指向另一条受管技能路径）并不等价，两处不对称是
	// 既有行为，原样保留。
	linked, err := isLinkedSkillDir(path.Dir)
	if err != nil {
		return nil, err
	}
	if linked {
		skip := SkipLinkedDir
		return &skip, nil
	}
	return nil, nil
}

// skillDirLinkEntry：skillDirLinks 的三元组元素。
type skillDirLinkEntry struct {
	name string
	path *ResolvedSkillPath
	link SkillDirLink
}

// skillDirLinks：全部技能路径的落点链接状态（只读探测）：(条目名, path,
// 链接状态) 三元组一次给出，调用方不再各自把两条独立序列配对。落点归属
// 与参与判定都从这里取，不给调用方各自 canonicalize 的机会——落点是不是
// 链接、内容属于谁、解出的真实目录，答案只有一处。
func (c *ResolvedManagerConfig) skillDirLinks() ([]skillDirLinkEntry, error) {
	// 受管落点 → 条目名。归属比较按「链接自身路径」算：对目录链接
	// canonicalize 会解析到另一端，别名就会与它的目标撞进同一个 key。
	dirToPath := map[string]string{}
	for _, name := range sortedKeys(c.SkillPaths) {
		path := c.SkillPaths[name]
		key := normalizeOwnDir(path.Dir)
		if _, exists := dirToPath[key]; !exists {
			dirToPath[key] = name
		}
	}

	entries := make([]skillDirLinkEntry, 0, len(c.SkillPaths))
	for _, name := range sortedKeys(c.SkillPaths) {
		path := c.SkillPaths[name]
		linked, err := readSkillDirLink(path.Dir)
		if err != nil {
			return nil, err
		}
		link := SkillDirLink{}
		if linked != nil {
			link.IsLink = true
			link.LinkTarget = *linked
			if owner, ok := dirToPath[*linked]; ok {
				link.LinkedPathID = owner
				link.HasLinkedPath = true
			}
		}
		entries = append(entries, skillDirLinkEntry{name: name, path: path, link: link})
	}
	return entries, nil
}
