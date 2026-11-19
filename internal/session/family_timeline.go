package session

// family_timeline.rs：族级摘要后处理与 agent 名册。分组与排序归 family_index，
// 本文件拥有 family 级摘要的后处理与名册装配；load-through 时间线缓存在
// reader_engine（按实例持有）。

// AgentMessages：子代理弹窗的数据口径——family 消息里按 session id 归属于
// 该子代理的部分（marker 消息与子会话消息同 session id）。
func AgentMessages(messages []SessionMessage, agentSessionID string) []SessionMessage {
	collected := make([]SessionMessage, 0)
	for i := range messages {
		if messages[i].SessionID != nil && *messages[i].SessionID == agentSessionID {
			collected = append(collected, messages[i])
		}
	}
	return collected
}

// FamilyAgentLabelKind：来源按成员给出的名册标签变体。is_root 由变体推导，
// 让标志与展示标签不可能互相矛盾。
type FamilyAgentLabelKind uint8

const (
	// AgentLabelRoot：族根。
	AgentLabelRoot FamilyAgentLabelKind = iota
	// AgentLabelChild：派生的子代理。
	AgentLabelChild
	// AgentLabelDerived：Codex fork——带源线程 id，展示为派生而非派生出的
	// 子代理。
	AgentLabelDerived
)

// FamilyAgentLabel：成员的标签声明。
type FamilyAgentLabel struct {
	Kind FamilyAgentLabelKind
	// Name：Child/Derived 的显示名；Root 为空。
	Name string
}

// familyAgentLabel：族标签的最简判定——族根行是 Root，其余行按会话标题标为
// Child（opencode/zcode 等单根来源共用）。
func familyAgentLabel(isRoot bool, name string) FamilyAgentLabel {
	if isRoot {
		return FamilyAgentLabel{Kind: AgentLabelRoot}
	}
	return FamilyAgentLabel{Kind: AgentLabelChild, Name: name}
}

// FamilyAgents：family 的 agent 名册。Codex 的 resume 段与原始段都标为
// Root 且共享 member id；root 先登记，可以去掉续跑段，同时保留 Claude 中
// 「子代理回退使用 family key」的条目。
func FamilyAgents[R FamilyRow](family Family[R], labelOf func(row R) FamilyAgentLabel) []SessionAgent {
	seenRootIDs := map[string]bool{}
	roster := make([]SessionAgent, 0, len(family.Members)+1)

	visit := func(row R) {
		label := labelOf(row)
		if label.Kind == AgentLabelRoot {
			memberID := row.MemberID()
			if seenRootIDs[memberID] {
				return
			}
			seenRootIDs[memberID] = true
		}

		var labelText string
		var isRoot bool
		switch label.Kind {
		case AgentLabelRoot:
			labelText, isRoot = "主 Agent", true
		case AgentLabelChild:
			labelText = label.Name + "(子)"
		case AgentLabelDerived:
			labelText = label.Name + "(派生)"
		}

		roster = append(roster, SessionAgent{
			SessionID: row.MemberID(),
			Label:     labelText,
			IsRoot:    isRoot,
		})
	}

	visit(family.Root)
	for i := range family.Members {
		visit(family.Members[i])
	}
	return roster
}
