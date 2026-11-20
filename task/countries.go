package task

import "strings"

// ColoCountry 把 Cloudflare 边缘机房三字码（IATA）映射到 ISO 3166-1 两位国家代码。
//
// 只收录 Cloudflare 实际有边缘节点的常见城市；未收录的 colo 返回空字符串，
// 调用方应当把「无法判断」视为「不排除」，避免误杀。
var ColoCountry = map[string]string{
	// 亚太
	"HKG": "HK",
	"TPE": "TW", "KHH": "TW",
	"NRT": "JP", "KIX": "JP", "ITM": "JP", "CTS": "JP", "FUK": "JP", "OKA": "JP",
	"ICN": "KR", "PUS": "KR",
	"SIN": "SG",
	"KUL": "MY",
	"BKK": "TH",
	"MNL": "PH",
	"HAN": "VN", "SGN": "VN",
	"CGK": "ID", "DPS": "ID",
	"DEL": "IN", "BOM": "IN", "MAA": "IN", "CCU": "IN", "HYD": "IN", "BLR": "IN",
	"SYD": "AU", "MEL": "AU", "PER": "AU", "BNE": "AU",
	"AKL": "NZ",
	"KTM": "NP",
	"DAC": "BD",
	"CMB": "LK",
	"PNH": "KH",
	"RGN": "MM",
	"VTE": "LA",
	"ULN": "MN",
	"ALA": "KZ", "TSE": "KZ",
	"TAS": "UZ",

	// 北美
	"LAX": "US", "SJC": "US", "SFO": "US", "SEA": "US", "ORD": "US",
	"DFW": "US", "IAD": "US", "EWR": "US", "JFK": "US", "ATL": "US",
	"MIA": "US", "DEN": "US", "PHX": "US", "BOS": "US", "PDX": "US",
	"MSP": "US", "DTW": "US", "CLT": "US", "LAS": "US", "SAN": "US",
	"IAH": "US", "SLC": "US", "TPA": "US", "MCO": "US", "PHL": "US",
	"BWI": "US", "CLE": "US", "STL": "US", "AUS": "US", "SMF": "US",
	"HNL": "US",
	"YYZ": "CA", "YVR": "CA", "YUL": "CA", "YYC": "CA",
	"MEX": "MX", "GDL": "MX",

	// 南美
	"GRU": "BR", "GIG": "BR",
	"EZE": "AR",
	"SCL": "CL",
	"BOG": "CO",
	"LIM": "PE",

	// 欧洲
	"LHR": "GB", "MAN": "GB", "LGW": "GB", "STN": "GB", "EDI": "GB",
	"FRA": "DE", "MUC": "DE", "BER": "DE", "DUS": "DE", "HAM": "DE",
	"CDG": "FR", "ORY": "FR", "MRS": "FR", "LYS": "FR",
	"AMS": "NL",
	"BRU": "BE",
	"MAD": "ES", "BCN": "ES",
	"FCO": "IT", "MXP": "IT", "LIN": "IT",
	"ZRH": "CH", "GVA": "CH",
	"VIE": "AT",
	"CPH": "DK",
	"ARN": "SE",
	"OSL": "NO",
	"HEL": "FI",
	"WAW": "PL",
	"PRG": "CZ",
	"BUD": "HU",
	"OTP": "RO",
	"SOF": "BG",
	"ATH": "GR",
	"DUB": "IE",
	"LIS": "PT",
	"IST": "TR",

	// 中东 / 非洲
	"DXB": "AE",
	"DOH": "QA",
	"RUH": "SA", "JED": "SA",
	"TLV": "IL",
	"CAI": "EG",
	"JNB": "ZA", "CPT": "ZA",
	"LOS": "NG",
	"NBO": "KE",
	"KHI": "PK", "LHE": "PK",
}

// CountryNames 是国家代码到中文名的映射，与前端展示保持一致。
var CountryNames = map[string]string{
	"HK": "香港", "TW": "台湾", "JP": "日本", "KR": "韩国", "SG": "新加坡",
	"MY": "马来西亚", "TH": "泰国", "PH": "菲律宾", "VN": "越南", "ID": "印度尼西亚",
	"IN": "印度", "AU": "澳大利亚", "NZ": "新西兰", "NP": "尼泊尔", "BD": "孟加拉国",
	"LK": "斯里兰卡", "KH": "柬埔寨", "MM": "缅甸", "LA": "老挝", "MN": "蒙古",
	"KZ": "哈萨克斯坦", "UZ": "乌兹别克斯坦",
	"US": "美国", "CA": "加拿大", "MX": "墨西哥",
	"BR": "巴西", "AR": "阿根廷", "CL": "智利", "CO": "哥伦比亚", "PE": "秘鲁",
	"GB": "英国", "DE": "德国", "FR": "法国", "NL": "荷兰", "BE": "比利时",
	"ES": "西班牙", "IT": "意大利", "CH": "瑞士", "AT": "奥地利", "DK": "丹麦",
	"SE": "瑞典", "NO": "挪威", "FI": "芬兰", "PL": "波兰", "CZ": "捷克",
	"HU": "匈牙利", "RO": "罗马尼亚", "BG": "保加利亚", "GR": "希腊", "IE": "爱尔兰",
	"PT": "葡萄牙", "TR": "土耳其",
	"AE": "阿联酋", "QA": "卡塔尔", "SA": "沙特", "IL": "以色列", "EG": "埃及",
	"ZA": "南非", "NG": "尼日利亚", "KE": "肯尼亚", "PK": "巴基斯坦",
}

// CountryOption 是前端多选用的选项。
type CountryOption struct {
	Value string `json:"value"`
	Title string `json:"title"`
}

// CountryOptions 返回国家选项（按中文名排序，供 API/前端使用）。
func CountryOptions() []CountryOption {
	// 常见国家优先，其余按字母序。
	order := []string{
		"HK", "TW", "JP", "KR", "SG", "US", "CA",
		"GB", "DE", "FR", "NL", "AU", "IN",
		"MY", "TH", "PH", "VN", "ID", "BR", "MX",
		"AE", "TR", "IT", "ES", "SE", "PL", "CH", "AT", "BE", "DK",
		"NO", "FI", "IE", "PT", "GR", "CZ", "HU", "RO", "BG",
		"NZ", "AR", "CL", "CO", "PE", "SA", "QA", "IL", "EG", "ZA",
		"NG", "KE", "PK", "BD", "LK", "KH", "MM", "LA", "MN", "KZ", "UZ", "NP",
	}
	out := make([]CountryOption, 0, len(order))
	for _, code := range order {
		if name, ok := CountryNames[code]; ok {
			out = append(out, CountryOption{Value: code, Title: name})
		}
	}
	return out
}

// CountryOfColo 返回 colo 所属国家代码；无法识别时返回空字符串。
func CountryOfColo(colo string) string {
	colo = strings.ToUpper(strings.TrimSpace(colo))
	if colo == "" || colo == "N/A" {
		return ""
	}
	// 兼容直接传国家代码（前端旧数据可能存的是 US/JP）。
	if len(colo) == 2 {
		if _, ok := CountryNames[colo]; ok {
			return colo
		}
	}
	return ColoCountry[colo]
}

// NormalizeCountryFilter 清洗国家筛选：
// 大写、去重、只保留已知国家代码（未知项直接丢弃，避免用户误配置后永远筛不到）。
func NormalizeCountryFilter(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if len(code) != 2 {
			continue
		}
		if _, ok := CountryNames[code]; !ok {
			continue
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MatchCountry 判断一个 colo 是否命中国家筛选。
//
// filter 为空表示不筛选，全部通过。
// colo 无法识别（空 / N/A / 未收录）时返回 true：宁可多保留，也不误杀
// 一个可能可用的 IP；前端会在结果里把地区显示为 N/A。
func MatchCountry(colo string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	country := CountryOfColo(colo)
	if country == "" {
		return true
	}
	for _, code := range filter {
		if country == strings.ToUpper(code) {
			return true
		}
	}
	return false
}
