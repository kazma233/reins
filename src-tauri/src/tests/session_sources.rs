use crate::session;
use crate::session::model::SourceApp;

// 来源注册表是全部来源分发的唯一事实:必须恰好覆盖每个 SourceApp 变体
// 一次;顺序是可观察行为(detect 输出与 usage sources 都按该顺序产出),
// 等于历史逐来源 join 的顺序,不得随意重排。
#[test]
fn sources_registry_covers_every_source_app_once_in_order() {
    let listed: Vec<SourceApp> = session::sources::SOURCES
        .iter()
        .map(|spec| spec.app)
        .collect();

    let expected = [
        SourceApp::Codex,
        SourceApp::ClaudeCode,
        SourceApp::OpenCode,
        SourceApp::Pi,
        SourceApp::GrokBuild,
        SourceApp::Zcode,
        SourceApp::Dsh,
    ];

    assert_eq!(listed, expected);

    // spec() 的 match 必须与表同源:表按值持副本,这里校验每个变体都路由到
    // 描述自己的条目,漏登记或错位都先在这里红。
    for app in expected {
        assert_eq!(session::sources::spec(app).app, app);
    }
}
