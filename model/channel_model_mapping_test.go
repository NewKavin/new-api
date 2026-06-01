package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func clearChannelAbilityTables(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
}

func hasAbilityModel(t *testing.T, channelID int, modelName string) bool {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&Ability{}).
		Where("channel_id = ? and model = ?", channelID, modelName).
		Count(&count).Error)
	return count > 0
}

func TestChannelGetAbilityModelsIncludesModelMappingAliases(t *testing.T) {
	modelMapping := `{"openai/gpt-5-codex":"glm-4-plus","gpt-5-codex":"deepseek-chat","bad-empty":"  ","":"glm-4-plus"}`
	channel := &Channel{
		Models:       "glm-4-plus, deepseek-chat",
		ModelMapping: &modelMapping,
	}

	models := channel.GetAbilityModels()
	require.ElementsMatch(t,
		[]string{"glm-4-plus", "deepseek-chat", "openai/gpt-5-codex", "gpt-5-codex"},
		models,
	)
}

func TestChannelInsertAddsModelMappingAliasAbilities(t *testing.T) {
	clearChannelAbilityTables(t)

	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
	})

	modelMapping := `{"openai/gpt-5-codex":"glm-4-plus","gpt-5-codex":"glm-4-plus"}`
	channel := &Channel{
		Id:           9101,
		Type:         constant.ChannelTypeOpenAI,
		Key:          "sk-test",
		Status:       common.ChannelStatusEnabled,
		Name:         "model-mapping-alias-db",
		Models:       "glm-4-plus",
		Group:        "default",
		ModelMapping: &modelMapping,
	}
	require.NoError(t, channel.Insert())

	require.True(t, hasAbilityModel(t, channel.Id, "glm-4-plus"))
	require.True(t, hasAbilityModel(t, channel.Id, "openai/gpt-5-codex"))
	require.True(t, IsChannelEnabledForGroupModel("default", "openai/gpt-5-codex", channel.Id))
}

func TestGetRandomSatisfiedChannelSupportsModelMappingAliasWithCache(t *testing.T) {
	clearChannelAbilityTables(t)

	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
	})

	modelMapping := `{"openai/gpt-5-codex":"glm-4-plus","gpt-5-codex":"glm-4-plus"}`
	channel := &Channel{
		Id:           9102,
		Type:         constant.ChannelTypeOpenAI,
		Key:          "sk-test",
		Status:       common.ChannelStatusEnabled,
		Name:         "model-mapping-alias-cache",
		Models:       "glm-4-plus",
		Group:        "default",
		ModelMapping: &modelMapping,
	}
	require.NoError(t, channel.Insert())

	InitChannelCache()
	selected, err := GetRandomSatisfiedChannel("default", "openai/gpt-5-codex", 0)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, channel.Id, selected.Id)
}

func TestEditChannelByTagRebuildsAbilitiesWhenModelMappingChanges(t *testing.T) {
	clearChannelAbilityTables(t)

	tag := "model-mapping-rebuild-tag"
	channel := &Channel{
		Id:     9103,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "sk-test",
		Status: common.ChannelStatusEnabled,
		Name:   "model-mapping-edit-tag",
		Models: "glm-4-plus",
		Group:  "default",
		Tag:    &tag,
	}
	require.NoError(t, channel.Insert())
	require.False(t, hasAbilityModel(t, channel.Id, "openai/gpt-5-codex"))

	modelMapping := `{"openai/gpt-5-codex":"glm-4-plus"}`
	require.NoError(t, EditChannelByTag(tag, nil, &modelMapping, nil, nil, nil, nil, nil, nil))
	require.True(t, hasAbilityModel(t, channel.Id, "openai/gpt-5-codex"))
}
