package task

import "strings"

// ColoNames 是 Cloudflare 边缘机房三字码到中文城市名的映射。
// 与前端 ResultTable.vue 的 COLO_NAMES 保持一致，导出 TXT 时用中文备注。
var ColoNames = map[string]string{
	// 亚太
	"HKG": "香港", "SIN": "新加坡",
	"NRT": "东京", "KIX": "大阪", "ITM": "大阪", "CTS": "札幌", "FUK": "福冈", "OKA": "冲绳",
	"TPE": "台北", "KHH": "高雄",
	"ICN": "首尔", "PUS": "釜山",
	"KUL": "吉隆坡", "BKK": "曼谷", "MNL": "马尼拉", "HAN": "河内", "SGN": "胡志明",
	"CGK": "雅加达", "DPS": "巴厘岛",
	"DEL": "新德里", "BOM": "孟买", "MAA": "金奈", "CCU": "加尔各答", "HYD": "海得拉巴", "BLR": "班加罗尔",
	"SYD": "悉尼", "MEL": "墨尔本", "AKL": "奥克兰", "PER": "珀斯", "BNE": "布里斯班",
	"KTM": "加德满都", "DAC": "达卡", "CMB": "科伦坡", "PNH": "金边", "RGN": "仰光",
	"VTE": "万象", "ULN": "乌兰巴托", "ALA": "阿拉木图", "TSE": "阿斯塔纳", "TAS": "塔什干",

	// 北美
	"LAX": "洛杉矶", "SJC": "圣何塞", "SFO": "旧金山", "SEA": "西雅图",
	"ORD": "芝加哥", "IAD": "华盛顿", "EWR": "纽约", "JFK": "纽约",
	"ATL": "亚特兰大", "DFW": "达拉斯", "MIA": "迈阿密", "DEN": "丹佛",
	"PHX": "凤凰城", "BOS": "波士顿", "PDX": "波特兰", "MSP": "明尼阿波利斯",
	"DTW": "底特律", "CLT": "夏洛特", "LAS": "拉斯维加斯", "SAN": "圣地亚哥",
	"IAH": "休斯顿", "SLC": "盐湖城", "TPA": "坦帕", "MCO": "奥兰多",
	"PHL": "费城", "BWI": "巴尔的摩", "CLE": "克利夫兰", "STL": "圣路易斯",
	"AUS": "奥斯汀", "SMF": "萨克拉门托", "HNL": "檀香山",
	"YYZ": "多伦多", "YVR": "温哥华", "YUL": "蒙特利尔", "YYC": "卡尔加里",
	"MEX": "墨西哥城", "GDL": "瓜达拉哈拉",

	// 南美
	"GRU": "圣保罗", "GIG": "里约热内卢",
	"EZE": "布宜诺斯艾利斯", "SCL": "圣地亚哥", "BOG": "波哥大", "LIM": "利马",

	// 欧洲
	"LHR": "伦敦", "MAN": "曼彻斯特", "LGW": "伦敦", "STN": "伦敦", "EDI": "爱丁堡",
	"FRA": "法兰克福", "MUC": "慕尼黑", "BER": "柏林", "DUS": "杜塞尔多夫", "HAM": "汉堡",
	"CDG": "巴黎", "ORY": "巴黎", "MRS": "马赛", "LYS": "里昂",
	"AMS": "阿姆斯特丹", "BRU": "布鲁塞尔",
	"MAD": "马德里", "BCN": "巴塞罗那",
	"FCO": "罗马", "MXP": "米兰", "LIN": "米兰",
	"ZRH": "苏黎世", "GVA": "日内瓦", "VIE": "维也纳",
	"CPH": "哥本哈根", "ARN": "斯德哥尔摩", "OSL": "奥斯陆", "HEL": "赫尔辛基",
	"WAW": "华沙", "PRG": "布拉格", "BUD": "布达佩斯", "OTP": "布加勒斯特",
	"SOF": "索菲亚", "ATH": "雅典", "DUB": "都柏林", "LIS": "里斯本",
	"IST": "伊斯坦布尔",

	// 中东 / 非洲
	"DXB": "迪拜", "DOH": "多哈", "RUH": "利雅得", "JED": "吉达",
	"TLV": "特拉维夫", "CAI": "开罗",
	"JNB": "约翰内斯堡", "CPT": "开普敦", "LOS": "拉各斯", "NBO": "内罗毕",
	"KHI": "卡拉奇", "LHE": "拉合尔",

	// 中国大陆（CF 一般不给大陆分配边缘节点，留作兜底）
	"PEK": "北京", "SHA": "上海", "CAN": "广州", "SZX": "深圳",
}

// ColoLabel 返回中文城市名；未收录的代码原样返回，避免导出空白。
func ColoLabel(colo string) string {
	c := strings.ToUpper(strings.TrimSpace(colo))
	if c == "" || c == "N/A" {
		return "N/A"
	}
	if name, ok := ColoNames[c]; ok {
		return name
	}
	return c
}
