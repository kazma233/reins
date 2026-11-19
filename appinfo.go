// AppInfoService：应用元信息的 Wails service。前端关于页的版本号经
// Version() 读取；版本号共三处（本常量、build/config.yml 的 info.version、
// frontend/package.json 的 version），必须同步修改，一致性由
// appinfo_test.go 的 TestVersionConsistency 守卫。
package main

// AppInfoService 无状态，全部方法值语义。
type AppInfoService struct{}

// Version 返回应用版本号（三处同步，见 appinfo_test.go 守卫）。
func (s *AppInfoService) Version() string { return "0.3.0" }
