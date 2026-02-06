package platform

import (
	"net/url"
	"strings"
)

const (
	PlatformDouyin   = "douyin"
	PlatformKuaishou = "kuaishou"
	PlatformBilibili = "bilibili"
	PlatformXHS      = "xiaohongshu"
	PlatformHaokan   = "haokan"
	PlatformWeishi   = "weishi"
	PlatformPear     = "pearvideo"
	PlatformPipigx   = "pipigaoxiao"
)

func Detect(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Host)
	switch {
	case strings.Contains(host, "douyin") || strings.Contains(host, "iesdouyin"):
		return PlatformDouyin, true
	case strings.Contains(host, "kuaishou") || strings.Contains(host, "kwai"):
		return PlatformKuaishou, true
	case strings.Contains(host, "bilibili") || strings.Contains(host, "b23.tv"):
		return PlatformBilibili, true
	case strings.Contains(host, "xiaohongshu") || strings.Contains(host, "xhslink"):
		return PlatformXHS, true
	case strings.Contains(host, "haokan.baidu.com") || strings.Contains(host, "haokan.hao123.com"):
		return PlatformHaokan, true
	case strings.Contains(host, "weishi.qq.com") || strings.Contains(host, "isee.weishi"):
		return PlatformWeishi, true
	case strings.Contains(host, "pearvideo"):
		return PlatformPear, true
	case strings.Contains(host, "pipigx"):
		return PlatformPipigx, true
	default:
		return "", false
	}
}
