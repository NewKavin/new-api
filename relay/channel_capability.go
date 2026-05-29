package relay

import "github.com/QuantumNous/new-api/constant"

func SupportsNativeResponses(apiType int, channelType int) bool {
	switch apiType {
	case constant.APITypeOpenAI,
		constant.APITypeAli,
		constant.APITypePerplexity,
		constant.APITypeCloudflare,
		constant.APITypeVolcEngine,
		constant.APITypeXai,
		constant.APITypeSubmodel,
		constant.APITypeCodex,
		constant.APITypeOpenRouter,
		constant.APITypeXinference:
		return true
	default:
		return false
	}
}

func SupportsResponsesViaChat(apiType int, channelType int) bool {
	switch channelType {
	case constant.ChannelTypeMiniMax, constant.ChannelTypeZhipu_v4:
		return true
	}
	switch apiType {
	case constant.APITypeMiniMax, constant.APITypeZhipuV4:
		return true
	default:
		return false
	}
}
