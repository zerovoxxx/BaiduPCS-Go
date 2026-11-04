package baidu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
)

// ValidateAccount uses only this instance's HTTPS client. A PCS access token can
// prove PCS access without proving an identity or a cookie-only Pan capability.
func (f *ClientFactory) ValidateAccount(ctx context.Context, snapshot configuration.Snapshot, c account.Credentials) (account.Identity, error) {
	var identity account.Identity
	s, err := f.New(ctx, snapshot, c)
	if err != nil {
		return identity, err
	}
	defer s.Close()
	if c.AccessToken != "" && len(c.Cookies) == 0 {
		return identity, validateTokenQuota(ctx, s, snapshot.Client(), c.AccessToken)
	}
	if _, _, err := s.PCS.QuotaInfo(); err != nil {
		return identity, upstreamFault(err)
	}
	body, err := s.PCS.PrepareUK()
	if err != nil {
		return identity, upstreamFault(err)
	}
	defer body.Close()
	var info struct {
		Errno   int `json:"errno"`
		Records []struct {
			UK        json.Number `json:"uk"`
			Name      string      `json:"uname"`
			BaiduName string      `json:"baidu_name"`
		} `json:"records"`
	}
	if err = json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&info); err != nil {
		return identity, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度身份响应格式异常")
	}
	if info.Errno != 0 {
		return identity, fault.New(fault.Unsupported, "BAIDU_CREDENTIAL_EXPIRED", "百度会话不可用，请重新验证凭据")
	}
	if len(info.Records) != 1 {
		return identity, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度身份响应缺少唯一的当前账号记录")
	}
	user := info.Records[0]
	if uk, err := strconv.ParseUint(string(user.UK), 10, 64); err == nil && uk > 0 {
		value := strconv.FormatUint(uk, 10)
		identity.UK = &value
		identity.Verified = true
	}
	if !identity.Verified {
		return identity, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度身份响应缺少有效UK")
	}
	if user.Name == "" {
		user.Name = user.BaiduName
	}
	if user.Name != "" {
		identity.Username = &user.Name
	}
	return identity, nil
}

func validateTokenQuota(ctx context.Context, s *Session, cfg configuration.Client, token string) error {
	// The legacy SetaccessToken only applies to rapid-upload requests. Do not
	// mistake a cookie-only QuotaInfo call for validation of an OAuth token.
	u := s.PCS.URL()
	u.Path = "/rest/2.0/pcs/quota"
	query := u.Query()
	query.Set("method", "info")
	query.Set("app_id", strconv.Itoa(cfg.AppID))
	query.Set("access_token", token)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fault.New(fault.Internal, "INTERNAL_ERROR", "无法构造凭据验证请求")
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	response, err := s.HTTP.Client.Do(req)
	if err != nil {
		return upstreamFault(err)
	}
	defer response.Body.Close()
	var result struct {
		Code  int          `json:"error_code"`
		Quota *json.Number `json:"quota"`
		Used  *json.Number `json:"used"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result) != nil {
		return fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度token验证响应格式异常")
	}
	if result.Code == 110 || result.Code == 111 {
		return fault.New(fault.Unsupported, "BAIDU_CREDENTIAL_EXPIRED", "百度access token无效或已过期")
	}
	if result.Code != 0 {
		return fault.New(fault.Unsupported, "BAIDU_TOKEN_REJECTED", "百度未授予该token的PCS容量访问能力")
	}
	if response.StatusCode != 200 || result.Quota == nil || result.Used == nil {
		return fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度token验证未返回有效容量结构")
	}
	if _, err := strconv.ParseUint(string(*result.Quota), 10, 64); err != nil {
		return fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度容量字段无效")
	}
	if _, err := strconv.ParseUint(string(*result.Used), 10, 64); err != nil {
		return fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度容量字段无效")
	}
	return nil
}
