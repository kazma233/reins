// addressing.go：workspace 的寻址层。TargetRef 是复合地址的唯一定义，
// ResolvedManagerConfig 的五个寻址入口（target / targetByID / targetRefs /
// managedRefs / skipReason）与落点归属（skillDirLinks）只在这里。types.go
// 只留数据形状；「地址怎么解析、谁参与分发」的判定不与 DTO 同居。
package workspace

import (
	"fmt"

	"reins/internal/support"
)

// SkillDirLink：落点链接的探测结果。链接另一端是受管 target 时的 id、解出
// 的真实目录（不是链接时 nil）。复合地址由调用方随三元组携带。
type SkillDirLink struct {
	// LinkedTargetID：链接的另一端是某个受管 target 时给出它的 id；外部
	// 目录/断链时为 nil。
	LinkedTargetID  string
	HasLinkedTarget bool
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
	if l.HasLinkedTarget {
		id := l.LinkedTargetID
		view.LinkedTargetID = &id
	}
	view.LinkTarget = support.DisplayPath(l.LinkTarget)
	return view
}

// TargetSkip：目标不参与分发的原因。调用方只决定表现方式（报错 / 提示 /
// 静默），「为什么不参与」由这里回答。
type TargetSkip int

const (
	// SkipMissing：地址不存在：输入来自客户端（弹窗快照、命令参数）时才会
	// 出现。
	SkipMissing TargetSkip = iota + 1
	// SkipLinkedDir：skill_dir 整层是一个目录链接（指向另一个受管 target、
	// 外部目录或已断链）：内容由链接的另一端负责，本 target 不参与安装与
	// 清理。
	SkipLinkedDir
)

// targetRef：一个 target 的地址。全局 target 直接用 id；项目级 target 需要
// (项目, agent) 两段。ProjectID 为空串即全局。复合 id 的构造与拆分都只在
// addressing.go（parseTargetID / CompositeID）。
type targetRef struct {
	projectID string
	agentID   AgentTargetID
}

// CompositeID：面向 UI 与命令的稳定字符串：全局 = id，项目级 = `project:agent`。
func (r targetRef) CompositeID() string {
	if r.projectID == "" {
		return string(r.agentID)
	}
	return fmt.Sprintf("%s:%s", r.projectID, r.agentID)
}

// parseTargetID：`project:agent` 拆回两段；不含分隔符时按全局 id 处理。两段
// 原样保留，不做规范化：查不到就是查不到，不能悄悄映射到另一个 target。
func parseTargetID(compositeID string) targetRef {
	projectID, agentID, found := cutComposite(compositeID)
	if found && projectID != "" && agentID != "" {
		return targetRef{projectID: projectID, agentID: AgentTargetID(agentID)}
	}
	return targetRef{agentID: AgentTargetID(compositeID)}
}

// cutComposite：按第一个 ':' 拆分（Rust str::split_once 的等价物）。
func cutComposite(value string) (string, string, bool) {
	for i := 0; i < len(value); i++ {
		if value[i] == ':' {
			return value[:i], value[i+1:], true
		}
	}
	return value, "", false
}

// targetUnavailable：「target 不可用」的唯一说法：调用方只需决定「报错还是
// 记一条 warning」，不必各自措辞。
func targetUnavailable(compositeID string) error {
	return fmt.Errorf("target 不存在：%s", compositeID)
}

// target：唯一的寻址入口。调用方拿到的是配置里的真实 target，不是重新拼
// 出来的。缺失返回 nil。
func (c *ResolvedManagerConfig) target(reference targetRef) *ResolvedTargetConfig {
	if reference.projectID == "" {
		return c.Targets[reference.agentID]
	}
	project, ok := c.Projects[reference.projectID]
	if !ok {
		return nil
	}
	return project.Agents[reference.agentID]
}

// targetByID：复合 id 直达寻址；不存在返回 nil。
func (c *ResolvedManagerConfig) targetByID(compositeID string) *ResolvedTargetConfig {
	return c.target(parseTargetID(compositeID))
}

// targetRefEntry：targetRefs 的三元组元素（地址 + 指针）。
type targetRefEntry struct {
	reference targetRef
	target    *ResolvedTargetConfig
}

// targetRefs：全部 target 及其地址，顺序为「全局在前（id 字典序）、项目按
// 配置顺序（项目 id 字典序，agent id 字典序）」。BTreeMap 的 Go 对应口径。
// 枚举与批量操作都从这里取；需要展示串时再调 CompositeID，拆串只发生在
// parseTargetID。
func (c *ResolvedManagerConfig) targetRefs() []targetRefEntry {
	refs := make([]targetRefEntry, 0, len(c.Targets)+len(c.Projects))
	for _, id := range sortedTargetIDs(c.Targets) {
		refs = append(refs, targetRefEntry{reference: targetRef{agentID: id}, target: c.Targets[id]})
	}
	for _, projectID := range sortedKeys(c.Projects) {
		project := c.Projects[projectID]
		for _, agentID := range sortedTargetIDs(project.Agents) {
			refs = append(refs, targetRefEntry{
				reference: targetRef{projectID: projectID, agentID: agentID},
				target:    project.Agents[agentID],
			})
		}
	}
	return refs
}

// skipReason：该 target 是否参与技能分发。判定只有这一处；调用方只决定
// 「跳过时怎么表现」（报错 / warning / 静默）。MCP 分发与状态展示不经过它
// ——那条链路问的是 config_path 而不是 skill_dir。
//
// 为什么放在寻址层而不是安装面：它回答的是「这个地址可用吗」，是对
// targetRef 的补充，而不是技能安装的私有规则。
func (c *ResolvedManagerConfig) skipReason(reference targetRef) (*TargetSkip, error) {
	target := c.target(reference)
	if target == nil {
		missing := SkipMissing
		return &missing, nil
	}
	// 项目级 target 的 skill_dir 整层指向别处时，内容由链接的另一端负责，
	// 这里不自作主张。
	//
	// 注意：这里只看「是不是链接」，不看「指向谁」；与展示层的
	// linkedTargetID（只看指向另一个受管 target、且对全局 target 也生效）
	// 并不等价，两处不对称是既有行为，原样保留。
	if target.IsProject {
		linked, err := isLinkedSkillDir(target.SkillDir)
		if err != nil {
			return nil, err
		}
		if linked {
			skip := SkipLinkedDir
			return &skip, nil
		}
	}
	return nil, nil
}

// skillDirLinkEntry：skillDirLinks 的三元组元素。
type skillDirLinkEntry struct {
	reference targetRef
	target    *ResolvedTargetConfig
	link      SkillDirLink
}

// skillDirLinks：全部 target 的落点链接状态（只读探测）：(地址, target,
// 链接状态) 三元组一次给出，调用方不再各自把两条独立序列配对。落点归属与
// 参与判定都从这里取，不给调用方各自 canonicalize 的机会——落点是不是链接、
// 内容属于谁、解出来的真实目录，答案只有一处。
func (c *ResolvedManagerConfig) skillDirLinks() ([]skillDirLinkEntry, error) {
	// 受管落点 → target id。归属比较按「链接自身路径」算：对目录链接
	// canonicalize 会解析到另一端，别名就会与它的目标撞进同一个 key。
	dirToTarget := map[string]string{}
	for _, entry := range c.targetRefs() {
		key := normalizeOwnDir(entry.target.SkillDir)
		if _, exists := dirToTarget[key]; !exists {
			dirToTarget[key] = entry.reference.CompositeID()
		}
	}

	entries := make([]skillDirLinkEntry, 0, len(c.Targets)+len(c.Projects))
	for _, entry := range c.targetRefs() {
		linked, err := readSkillDirLink(entry.target.SkillDir)
		if err != nil {
			return nil, err
		}
		link := SkillDirLink{}
		if linked != nil {
			link.IsLink = true
			link.LinkTarget = *linked
			if owner, ok := dirToTarget[*linked]; ok {
				link.LinkedTargetID = owner
				link.HasLinkedTarget = true
			}
		}
		entries = append(entries, skillDirLinkEntry{reference: entry.reference, target: entry.target, link: link})
	}
	return entries, nil
}

// managedRefEntry：managedRefs 的三元组元素（skip 为 nil 表示参与分发）。
type managedRefEntry struct {
	reference targetRef
	target    *ResolvedTargetConfig
	skip      *TargetSkip
}

// managedRefs：全部 target 的 (地址, target, 是否参与分发)。
func (c *ResolvedManagerConfig) managedRefs() ([]managedRefEntry, error) {
	refs := c.targetRefs()
	entries := make([]managedRefEntry, 0, len(refs))
	for _, entry := range refs {
		skip, err := c.skipReason(entry.reference)
		if err != nil {
			return nil, err
		}
		entries = append(entries, managedRefEntry{reference: entry.reference, target: entry.target, skip: skip})
	}
	return entries, nil
}
