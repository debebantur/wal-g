package config_test

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/wal-g/tracelog"
	"github.com/wal-g/wal-g/internal"
	"github.com/wal-g/wal-g/internal/config"
)

func TestGetMaxConcurrency_InvalidKey(t *testing.T) {
	_, err := config.GetMaxConcurrency("INVALID_KEY")

	assert.Error(t, err)
}

func TestGetMaxConcurrency_ValidKey(t *testing.T) {
	viper.Set(config.UploadConcurrencySetting, "100")
	actual, err := config.GetMaxConcurrency(config.UploadConcurrencySetting)

	assert.NoError(t, err)
	assert.Equal(t, 100, actual)
	resetToDefaults()
}

func TestGetMaxConcurrency_ValidKeyAndNegativeValue(t *testing.T) {
	viper.Set(config.UploadConcurrencySetting, "-5")
	_, err := config.GetMaxConcurrency(config.UploadConcurrencySetting)

	assert.Error(t, err)
	resetToDefaults()
}

func TestGetMaxConcurrency_ValidKeyAndInvalidValue(t *testing.T) {
	viper.Set(config.UploadConcurrencySetting, "invalid")
	_, err := config.GetMaxConcurrency(config.UploadConcurrencySetting)

	assert.Error(t, err)
	resetToDefaults()
}

func TestConfigureLogging_WhenLogLevelSettingIsNotSet(t *testing.T) {
	assert.NoError(t, config.ConfigureLogging())
}

func TestConfigureLogging_WhenLogLevelSettingIsSet(t *testing.T) {
	viper.Set(config.LogLevelSetting, "someOtherLevel")
	err := config.ConfigureLogging()

	assert.Error(t, tracelog.UpdateLogLevel(viper.GetString(config.LogLevelSetting)), err)
	resetToDefaults()
}

func resetToDefaults() {
	viper.Reset()
	internal.ConfigureSettings(config.PG)
	config.InitConfig()
	config.Configure()
}
