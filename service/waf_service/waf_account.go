package waf_service

import (
	"SamWaf/common/uuid"
	"SamWaf/common/zlog"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"SamWaf/utils"
	"SamWaf/wafdb/dialect"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const pwdTimeLayout = "2006-01-02 15:04:05"

type WafAccountService struct{}

var WafAccountServiceApp = new(WafAccountService)

// pwdHash 计算口令哈希（bcrypt，内含随机 per-password 盐）
func (receiver *WafAccountService) pwdHash(plain string) (string, error) {
	return utils.BcryptHash(plain)
}

// VerifyPassword 校验明文口令是否匹配存储哈希：bcrypt 走 bcrypt；存量裸 MD5 走旧算法(透明升级期间兼容)。
func (receiver *WafAccountService) VerifyPassword(stored, plain string) bool {
	if utils.IsBcryptHash(stored) {
		return utils.BcryptVerify(stored, plain)
	}
	return stored != "" && stored == utils.Md5String(plain+global.GWAF_DEFAULT_ACCOUNT_SALT)
}

// UpgradePasswordHash 透明升级：命中旧 MD5 的账户，登录成功后用 bcrypt 重算落库
// （同一口令，不改 PwdUpdateTime、不记历史）。
func (receiver *WafAccountService) UpgradePasswordHash(loginAccount, plain string) {
	newHash, err := receiver.pwdHash(plain)
	if err != nil || newHash == "" {
		return
	}
	global.GWAF_LOCAL_DB.Model(model.Account{}).Where("login_account = ?", loginAccount).Update("LoginPassword", newHash)
}

// ValidateNewPassword 校验新口令：复杂度 + 历史防重用
func (receiver *WafAccountService) ValidateNewPassword(loginAccount, newPlain string) error {
	if ok, msg := utils.ValidatePasswordComplexity(newPlain, utils.BuildPolicyFromConfig()); !ok {
		return errors.New(msg)
	}
	historyCount := int(global.GCONFIG_PWD_HISTORY_COUNT)
	if historyCount > 0 {
		var histories []model.AccountPwdHistory
		global.GWAF_LOCAL_DB.Where("login_account = ?", loginAccount).Order("CREATE_TIME desc").Limit(historyCount).Find(&histories)
		for _, h := range histories {
			// bcrypt 非确定性，不能按等值比较；逐条校验明文是否命中历史哈希(同时兼容存量 MD5)
			if receiver.VerifyPassword(h.PasswordHash, newPlain) {
				return fmt.Errorf("新密码不能与最近 %d 次使用过的密码相同", historyCount)
			}
		}
	}
	return nil
}

// recordPwdHistory 记录口令指纹并裁剪到最近 N 条
func (receiver *WafAccountService) recordPwdHistory(loginAccount, hash string) {
	historyCount := int(global.GCONFIG_PWD_HISTORY_COUNT)
	if historyCount <= 0 {
		return
	}
	global.GWAF_LOCAL_DB.Create(&model.AccountPwdHistory{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		LoginAccount: loginAccount,
		PasswordHash: hash,
	})
	var histories []model.AccountPwdHistory
	global.GWAF_LOCAL_DB.Where("login_account = ?", loginAccount).Order("CREATE_TIME desc").Find(&histories)
	if len(histories) > historyCount {
		for _, h := range histories[historyCount:] {
			global.GWAF_LOCAL_DB.Where("id = ?", h.Id).Delete(model.AccountPwdHistory{})
		}
	}
}

// AddApi 管理员新建账号：强制角色合法 + 口令复杂度校验，新建账号默认需首登改密。
// 绑定了权限组（GroupId 非空）时校验组存在，权限组查询随请求 ctx 走租户回调。
func (receiver *WafAccountService) AddApi(ctx context.Context, req request.WafAccountAddReq) error {
	if !enums.IsValidRole(req.Role) {
		return errors.New("请指定合法的账号角色")
	}
	if err := receiver.ValidateNewPassword(req.LoginAccount, req.LoginPassword); err != nil {
		return err
	}
	if err := receiver.checkGroupExists(ctx, req.GroupId); err != nil {
		return err
	}
	return receiver.createAccount(req.LoginAccount, req.Role, req.LoginPassword, req.Status, req.Remarks, global.GCONFIG_PWD_FORCE_CHANGE_DEFAULT == 1, req.GroupId)
}

// InitDefaultAccount 系统引导创建默认管理员（全新安装）。
// 安全加固：不再使用固定默认口令 admin868，改为生成强随机初始口令，写入 data/ 供用户查看，并强制首登改密。
func (receiver *WafAccountService) InitDefaultAccount() error {
	plainPwd, err := utils.GenerateRandomPassword(16)
	if err != nil || plainPwd == "" {
		// 极端兜底：随机生成失败时退回旧默认口令，但仍强制改密
		plainPwd = global.GWAF_DEFAULT_ACCOUNT_PWD
	}
	// 强制首登改密（needChange=true），不做复杂度校验避免严格策略下引导被锁死
	if e := receiver.createAccount(global.GWAF_DEFAULT_ACCOUNT, enums.ROLE_SUPER_ADMIN, plainPwd, 0, "系统初始化生成", true, ""); e != nil {
		return e
	}
	receiver.writeInitialPasswordFile(global.GWAF_DEFAULT_ACCOUNT, plainPwd)
	return nil
}

// writeInitialPasswordFile 全新安装时把随机初始口令写入 data/ 供用户查看，并打印日志提示。
func (receiver *WafAccountService) writeInitialPasswordFile(account, plainPwd string) {
	dir := filepath.Join(utils.GetCurrentDir(), "data")
	if err := os.MkdirAll(dir, 0700); err != nil {
		zlog.Error("创建 data 目录失败", err)
		return
	}
	fp := filepath.Join(dir, "initial_password.txt")
	content := fmt.Sprintf("SamWaf 初始管理员账号: %s\n初始随机口令: %s\n生成时间: %s\n请立即登录并修改密码，登录后本文件可删除。\n",
		account, plainPwd, time.Now().Format(pwdTimeLayout))
	if err := os.WriteFile(fp, []byte(content), 0600); err != nil {
		zlog.Error("写入初始口令文件失败", err)
		return
	}
	zlog.Info(fmt.Sprintf("首次安装已生成随机管理员初始口令（首次登录必须立即修改）：账号=%s 初始口令=%s ，也可查看文件 %s",
		account, plainPwd, fp))
}

// createAccount 内部统一建账逻辑（groupId 为空表示不绑定权限组，走旧四角色判定）
func (receiver *WafAccountService) createAccount(loginAccount, role, plainPwd string, status int, remarks string, needChange bool, groupId string) error {
	hash, err := receiver.pwdHash(plainPwd)
	if err != nil {
		return err
	}
	needChangeFlag := 0
	if needChange {
		needChangeFlag = 1
	}
	var bean = &model.Account{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		LoginAccount:       loginAccount,
		Role:               role,
		GroupId:            groupId,
		LoginPassword:      hash,
		Status:             status,
		NeedChangePassword: needChangeFlag,
		PwdUpdateTime:      time.Now().Format(pwdTimeLayout),
		Remarks:            remarks,
	}
	global.GWAF_LOCAL_DB.Create(bean)
	receiver.recordPwdHistory(loginAccount, hash)
	return nil
}

func (receiver *WafAccountService) CheckIsExistApi(req request.WafAccountAddReq) error {
	return global.GWAF_LOCAL_DB.First(&model.Account{}, "login_account = ? ", req.LoginAccount).Error
}
func (receiver *WafAccountService) ModifyApi(ctx context.Context, req request.WafAccountEditReq, operatorRole string) error {

	var target model.Account
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", req.Id).First(&target)
	if target.Id == "" {
		return errors.New("账号不存在")
	}
	// N7：非超级管理员不得编辑超级管理员账号（防止改名/改状态等干扰或锁定超管账号）。
	if enums.NormalizeRole(target.Role) == enums.ROLE_SUPER_ADMIN && enums.NormalizeRole(operatorRole) != enums.ROLE_SUPER_ADMIN {
		return errors.New("仅超级管理员可编辑超级管理员账号")
	}
	// 若修改了登录名，新名不得与其他账号重复
	if req.LoginAccount != target.LoginAccount {
		var dup model.Account
		global.GWAF_LOCAL_DB.Where("login_account = ? and id <> ?", req.LoginAccount, req.Id).First(&dup)
		if dup.Id != "" {
			return errors.New("当前登录账号已存在")
		}
	}
	// 权限组绑定：非空校验组存在；空串=解绑回落旧四角色判定（组字段总是随编辑写入）
	if err := receiver.checkGroupExists(ctx, req.GroupId); err != nil {
		return err
	}
	beanMap := map[string]interface{}{
		"LoginAccount": req.LoginAccount,
		"GroupId":      req.GroupId,
		"Status":       req.Status,
		"Remarks":      req.Remarks,
		"UPDATE_TIME":  customtype.JsonTime(time.Now()),
	}
	// 角色留空则不修改；非空必须合法
	if req.Role != "" {
		if !enums.IsValidRole(req.Role) {
			return errors.New("请指定合法的账号角色")
		}
		// N7 防提权：仅超级管理员可变更账号角色（对照被更新记录 target 的当前角色，杜绝 id/login_account 解耦绕过）。
		if req.Role != target.Role && enums.NormalizeRole(operatorRole) != enums.ROLE_SUPER_ADMIN {
			return errors.New("仅超级管理员可变更账号角色")
		}
		beanMap["Role"] = req.Role
	}
	if err := global.GWAF_LOCAL_DB.Model(model.Account{}).Where("id = ?", req.Id).Updates(beanMap).Error; err != nil {
		return err
	}
	// 权限组绑定变化（含绑定/解绑）后强制下线：令牌在登录时冗余了 GroupId 快照，
	// 旧令牌会继续按旧组判定权限，唯一可靠的收敛方式是作废全部会话让其重新登录
	if req.GroupId != target.GroupId {
		receiver.forceLogoutAccount(target.LoginAccount)
	}
	return nil
}

// checkGroupExists 绑定权限组前校验组存在（权限组随租户隔离，随请求 ctx 走租户回调过滤）。
// groupId 为空表示不绑定/解绑，直接放行。
func (receiver *WafAccountService) checkGroupExists(ctx context.Context, groupId string) error {
	if groupId == "" {
		return nil
	}
	var cnt int64
	if err := global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.PermissionGroup{}).
		Where("id = ?", groupId).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt == 0 {
		return errors.New("权限组不存在")
	}
	return nil
}

// forceLogoutAccount 强制某账号全部端（web/mobile）下线：
// 删除该账号全部 token_info 行，并逐个失效令牌缓存键（CACHE_TOKEN+AccessToken），
// 与登录处的旧会话清理同口径。
func (receiver *WafAccountService) forceLogoutAccount(loginAccount string) {
	var tokens []model.TokenInfo
	if err := global.GWAF_LOCAL_DB.Where("login_account = ?", loginAccount).Find(&tokens).Error; err != nil {
		zlog.Warn("强制下线读取账号令牌失败", "account", loginAccount, "error", err.Error())
	}
	for _, t := range tokens {
		if t.AccessToken == "" {
			continue
		}
		global.GCACHE_WAFCACHE.Remove(enums.CACHE_TOKEN + t.AccessToken)
	}
	if err := global.GWAF_LOCAL_DB.Where("login_account = ?", loginAccount).Delete(model.TokenInfo{}).Error; err != nil {
		zlog.Warn("强制下线删除账号令牌失败", "account", loginAccount, "error", err.Error())
	}
}

// ChangeMyPasswordApi 当前登录账号自助改密：校验旧密码、复杂度、历史防重用，成功后清除强制改密标记
func (receiver *WafAccountService) ChangeMyPasswordApi(loginAccount, oldPlain, newPlain string) error {
	var bean model.Account
	err := global.GWAF_LOCAL_DB.Where("login_account = ?", loginAccount).First(&bean).Error
	if err != nil {
		return errors.New("帐号信息不存在")
	}
	if !receiver.VerifyPassword(bean.LoginPassword, oldPlain) {
		return errors.New("旧密码不正确")
	}
	if receiver.VerifyPassword(bean.LoginPassword, newPlain) {
		return errors.New("新旧密码相同")
	}
	if err = receiver.ValidateNewPassword(loginAccount, newPlain); err != nil {
		return err
	}
	newHash, err := receiver.pwdHash(newPlain)
	if err != nil {
		return err
	}
	beanMap := map[string]interface{}{
		"LoginPassword":      newHash,
		"NeedChangePassword": 0,
		"PwdUpdateTime":      time.Now().Format(pwdTimeLayout),
		"UPDATE_TIME":        customtype.JsonTime(time.Now()),
	}
	if err = global.GWAF_LOCAL_DB.Model(model.Account{}).Where("id = ?", bean.Id).Updates(beanMap).Error; err != nil {
		return err
	}
	receiver.recordPwdHistory(loginAccount, newHash)
	return nil
}
func (receiver *WafAccountService) ResetPwdApi(req request.WafAccountResetPwdReq) error {

	var superAccount model.Account
	global.GWAF_LOCAL_DB.Where(dialect.Q("role")+" = ?", enums.ROLE_SUPER_ADMIN).First(&superAccount)
	if !receiver.VerifyPassword(superAccount.LoginPassword, req.LoginSuperPassword) {
		return errors.New("超级管理员密码不正确")
	}

	var bean model.Account
	global.GWAF_LOCAL_DB.Where("login_account = ?", req.LoginAccount).Find(&bean)
	if bean.Id == "" {
		return errors.New("帐号信息不存在")
	}

	if receiver.VerifyPassword(bean.LoginPassword, req.LoginNewPassword) {
		return errors.New("新旧密码相同")
	}

	// 复杂度 + 历史防重用校验
	if err := receiver.ValidateNewPassword(req.LoginAccount, req.LoginNewPassword); err != nil {
		return err
	}

	newHash, err := receiver.pwdHash(req.LoginNewPassword)
	if err != nil {
		return err
	}
	beanMap := map[string]interface{}{
		"LoginPassword":      newHash,
		"NeedChangePassword": 1, // 管理员重置后，目标账号下次登录需强制改密
		"PwdUpdateTime":      time.Now().Format(pwdTimeLayout),
		"UPDATE_TIME":        customtype.JsonTime(time.Now()),
	}
	err = global.GWAF_LOCAL_DB.Model(model.Account{}).Where("id = ?", bean.Id).Updates(beanMap).Error
	if err != nil {
		return err
	}
	receiver.recordPwdHistory(req.LoginAccount, newHash)
	return nil
}
// AccountView 账号的展示视图：在账号字段上附加所属权限组名，
// 前端列表/详情直接显示，免得按 group_id 逐个反查。
type AccountView struct {
	model.Account
	GroupName string `json:"group_name"` //所属权限组名（未绑定为空）
}

func (receiver *WafAccountService) GetDetailApi(ctx context.Context, req request.WafAccountDetailReq) AccountView {
	var bean model.Account
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id=?", req.Id).Find(&bean)
	views := []AccountView{{Account: bean}}
	receiver.fillGroupNames(ctx, views)
	return views[0]
}
func (receiver *WafAccountService) GetInfoByLoginApi(req request.WafLoginReq) model.Account {
	var bean model.Account
	global.GWAF_LOCAL_DB.Where("login_account=? ", req.LoginAccount).Find(&bean)
	return bean
}

// UpdateLastLoginInfo 登录成功后刷新账号上的「上次登录」信息
//
// 存在 core 库的 account 表上，而不是只依赖日志库的 login_history：
// 日志库会被数据保留策略清理，清完之后老账号不能被当成「首次登录」而漏掉来源变化提醒。
// 失败只记日志不返回错误：这是提醒功能的附属数据，不该让它把登录流程带崩。
func (receiver *WafAccountService) UpdateLastLoginInfo(loginAccount, ip, area, loginTime string) {
	if loginAccount == "" {
		return
	}
	err := global.GWAF_LOCAL_DB.Model(model.Account{}).Where("login_account = ?", loginAccount).Updates(map[string]interface{}{
		"last_login_ip":   ip,
		"last_login_area": area,
		"last_login_time": loginTime,
	}).Error
	if err != nil {
		zlog.Warn("更新账号上次登录信息失败", "error", err.Error())
	}
}

/*
*
通过登录account获取账号信息
*/
func (receiver *WafAccountService) GetInfoByLoginAccount(loginAccount string) model.Account {
	var bean model.Account
	global.GWAF_LOCAL_DB.Where("login_account=? ", loginAccount).Find(&bean)
	return bean
}

/*
*
 */
func (receiver *WafAccountService) IsExistDefaultAccount() bool {
	var total int64 = 0

	err := global.GWAF_LOCAL_DB.Model(&model.Account{}).Where("login_account=? and login_password =?", global.GWAF_DEFAULT_ACCOUNT, utils.Md5String(global.GWAF_DEFAULT_ACCOUNT_PWD+global.GWAF_DEFAULT_ACCOUNT_SALT)).Count(&total).Error
	if err == nil {
		if total > 0 {
			return true
		} else {
			return false
		}
	} else {
		return false
	}
}
func (receiver *WafAccountService) GetDetailByIdApi(id string) model.Account {
	var bean model.Account
	global.GWAF_LOCAL_DB.Where("id=?", id).Find(&bean)
	return bean
}
func (receiver *WafAccountService) GetListApi(ctx context.Context, req request.WafAccountSearchReq) ([]AccountView, int64, error) {
	var list []model.Account
	var total int64 = 0

	/*where条件*/
	var whereField = ""
	var whereValues []interface{}
	//where字段
	whereField = ""
	if len(req.LoginAccount) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " login_account=? "
	}
	//where字段赋值
	if len(req.LoginAccount) > 0 {
		whereValues = append(whereValues, req.LoginAccount)
	}

	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Account{}).Where(whereField, whereValues...).Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list)
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Account{}).Where(whereField, whereValues...).Count(&total)

	views := make([]AccountView, 0, len(list))
	for _, a := range list {
		views = append(views, AccountView{Account: a})
	}
	receiver.fillGroupNames(ctx, views)
	return views, total, nil
}

// fillGroupNames 一次批量查询把权限组名拼装到视图上（权限组随租户隔离，随请求 ctx 过滤）。
// 失败只记日志不阻断：组名是展示附属信息，不能让它把账号列表拖挂。
func (receiver *WafAccountService) fillGroupNames(ctx context.Context, views []AccountView) {
	ids := make([]string, 0, len(views))
	seen := make(map[string]struct{}, len(views))
	for i := range views {
		if views[i].GroupId == "" {
			continue
		}
		if _, ok := seen[views[i].GroupId]; ok {
			continue
		}
		seen[views[i].GroupId] = struct{}{}
		ids = append(ids, views[i].GroupId)
	}
	if len(ids) == 0 {
		return
	}
	var groups []model.PermissionGroup
	if err := global.GWAF_LOCAL_DB.WithContext(ctx).Select("id, group_name").
		Where("id IN ?", ids).Find(&groups).Error; err != nil {
		zlog.Warn("账号视图拼装权限组名失败", "error", err.Error())
		return
	}
	nameById := make(map[string]string, len(groups))
	for _, g := range groups {
		nameById[g.Id] = g.GroupName
	}
	for i := range views {
		views[i].GroupName = nameById[views[i].GroupId]
	}
}
func (receiver *WafAccountService) DelApi(req request.WafAccountDelReq) error {
	var bean model.Account
	err := global.GWAF_LOCAL_DB.Where("id = ?", req.Id).First(&bean).Error
	if err != nil {
		return err
	} else {
		var total int64
		global.GWAF_LOCAL_DB.Model(&model.Account{}).Count(&total)
		if total <= 1 {
			return errors.New("系统中只剩一个账号，不能删除")
		}
	}
	err = global.GWAF_LOCAL_DB.Where("id = ?", req.Id).Delete(model.Account{}).Error
	return err
}

// 获取系统所有账号数量
func (receiver *WafAccountService) GetAccountCountApi() (int64, error) {
	var total int64 = 0
	global.GWAF_LOCAL_DB.Model(&model.Account{}).Count(&total)
	return total, nil
}
