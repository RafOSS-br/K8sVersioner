package store

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

// Mock structures to simulate k8sversionerv1alpha1 types
type MockConfigSpec struct {
	GitRef string
}

type MockConfig struct {
	Name string
	Spec MockConfigSpec
}

type MockGitConfig struct {
	Name string
}

func TestCreateOrUpdateConfig(t *testing.T) {
	s := NewStore(10)

	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	// Create or update GitConfig
	err := s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// Create or update Config
	err = s.CreateOrUpdateConfig(config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Verify if the config was stored correctly in cfgMap
	c, ok := s.(*store).cfgMap.Load("test-config")
	assert.True(t, ok, "Config not found in cfgMap")

	storedConfig, ok := c.(*Config)
	assert.True(t, ok, "Unexpected type stored in cfgMap")
	assert.Equal(t, config, storedConfig.Cfg, "Stored Config does not match the original")

	// Verify association with GitConfigEntry in gitCfgMap
	gitEntryInterface, ok := s.(*store).gitCfgMap.Load("test-gitconfig")
	assert.True(t, ok, "GitConfigEntry not found in gitCfgMap")

	gitEntry, ok := gitEntryInterface.(*GitConfigEntry)
	assert.True(t, ok, "Unexpected type stored in gitCfgMap")

	assert.Equal(t, gitConfig, gitEntry.GitConfig, "Stored GitConfig does not match the original")

	// Verify that the Config is associated with the GitConfigEntry
	found := false
	for _, cfg := range gitEntry.Configs {
		if cfg.Cfg.Name == "test-config" {
			found = true
			break
		}
	}
	assert.True(t, found, "Config not associated with GitConfigEntry")
}

func TestCreateOrUpdateGitConfig(t *testing.T) {
	s := NewStore(10)

	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	// Create or update GitConfig
	err := s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// Verify if the GitConfig was stored correctly in gitCfgMap
	gc, ok := s.(*store).gitCfgMap.Load("test-gitconfig")
	assert.True(t, ok, "GitConfigEntry not found in gitCfgMap")

	storedGitConfigEntry, ok := gc.(*GitConfigEntry)
	assert.True(t, ok, "Unexpected type stored in gitCfgMap")

	assert.Equal(t, gitConfig, storedGitConfigEntry.GitConfig, "Stored GitConfig does not match the original")

	// Verify that the Configs slice is initially empty
	assert.Empty(t, storedGitConfigEntry.Configs, "Configs slice should be empty initially")
}

func TestBuddleProducedOnConfigCreation(t *testing.T) {
	s := NewStore(10)

	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	// Create or update GitConfig
	err := s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// Create or update Config
	err = s.CreateOrUpdateConfig(config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Wait for the Buddle to be produced
	select {
	case buddle := <-s.ConfigProducer():
		assert.Equal(t, config, buddle.Config.Cfg, "Config in Buddle does not match the expected")
		assert.Equal(t, gitConfig, buddle.GitConfig, "GitConfig in Buddle does not match the expected")
	case <-time.After(time.Second * 2):
		t.Fatal("Timeout waiting for Buddle")
	}
}

func TestCreateOrUpdateConfig_GitConfigNotFound(t *testing.T) {
	s := NewStore(10)

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "nonexistent-gitconfig",
		},
	}

	// Attempt to create Config without existing GitConfig
	err := s.CreateOrUpdateConfig(config)
	assert.ErrorIs(t, err, ErrGitConfigNotFound, "Expected ErrGitConfigNotFound when GitConfig does not exist")
}

func TestCreateOrUpdateConfig_UnexpectedType(t *testing.T) {
	s := NewStore(10)

	// Store a MockConfig instead of *Config
	s.(*store).cfgMap.Store("test-config", &MockConfig{Name: "test-config"})

	// Attempt to create Buddle by associating with MockConfig
	// Since CreateOrUpdateConfig expects a *k8sversionerv1alpha1.Config, we simulate a type mismatch scenario

	// To simulate, we attempt to retrieve the Config and manually trigger the error
	err := s.CreateOrUpdateConfig(&k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	})
	assert.ErrorIs(t, err, ErrGitConfigNotFound, "Expected ErrGitConfigNotFound due to type mismatch and missing GitConfig")
}

func TestCreateOrUpdateGitConfig_UpdatesAssociatedConfigs(t *testing.T) {
	s := NewStore(10)

	// Create Configs that reference the GitConfig
	config1 := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "config1",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	config2 := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "config2",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	// Attempt to create Configs before GitConfig exists
	err := s.CreateOrUpdateConfig(config1)
	assert.ErrorIs(t, err, ErrGitConfigNotFound, "Expected ErrGitConfigNotFound when creating Config without GitConfig")

	err = s.CreateOrUpdateConfig(config2)
	assert.ErrorIs(t, err, ErrGitConfigNotFound, "Expected ErrGitConfigNotFound when creating Config without GitConfig")

	// Now create GitConfig
	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	err = s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// Re-attempt to create Configs
	err = s.CreateOrUpdateConfig(config1)
	assert.NoError(t, err, "Creating Config1 should not produce an error")

	err = s.CreateOrUpdateConfig(config2)
	assert.NoError(t, err, "Creating Config2 should not produce an error")

	// Verify that both Configs are associated with the GitConfigEntry
	gitEntryInterface, ok := s.(*store).gitCfgMap.Load("test-gitconfig")
	assert.True(t, ok, "GitConfigEntry not found in gitCfgMap")

	gitEntry, ok := gitEntryInterface.(*GitConfigEntry)
	assert.True(t, ok, "Unexpected type stored in gitCfgMap")

	assert.Len(t, gitEntry.Configs, 2, "There should be two Configs associated with the GitConfigEntry")

	configNames := map[string]bool{
		"config1": false,
		"config2": false,
	}

	for _, cfg := range gitEntry.Configs {
		if _, exists := configNames[cfg.Cfg.Name]; exists {
			configNames[cfg.Cfg.Name] = true
		}
	}

	for name, found := range configNames {
		assert.True(t, found, "Config %s not associated with GitConfigEntry", name)
	}
}

func TestDeleteConfig(t *testing.T) {
	s := NewStore(10)

	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	// Create GitConfig and Config
	err := s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	err = s.CreateOrUpdateConfig(config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Delete Config
	err = s.DeleteConfig("test-config")
	assert.NoError(t, err, "Deleting Config should not produce an error")

	// Verify Config is removed from cfgMap
	_, ok := s.(*store).cfgMap.Load("test-config")
	assert.False(t, ok, "Config should have been deleted from cfgMap")

	// Verify Config is removed from GitConfigEntry
	gitEntryInterface, ok := s.(*store).gitCfgMap.Load("test-gitconfig")
	assert.True(t, ok, "GitConfigEntry not found in gitCfgMap")

	gitEntry, ok := gitEntryInterface.(*GitConfigEntry)
	assert.True(t, ok, "Unexpected type stored in gitCfgMap")

	found := false
	for _, cfg := range gitEntry.Configs {
		if cfg.Cfg.Name == "test-config" {
			found = true
			break
		}
	}
	assert.False(t, found, "Config should have been removed from GitConfigEntry")
}

func TestDeleteGitConfig(t *testing.T) {
	s := NewStore(10)

	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	// Create GitConfig and Config
	err := s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	err = s.CreateOrUpdateConfig(config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Delete GitConfig
	err = s.DeleteGitConfig("test-gitconfig")
	assert.NoError(t, err, "Deleting GitConfig should not produce an error")

	// Verify GitConfig is removed from gitCfgMap
	_, ok := s.(*store).gitCfgMap.Load("test-gitconfig")
	assert.False(t, ok, "GitConfigEntry should have been deleted from gitCfgMap")

	// Verify associated Config is also deleted from cfgMap
	_, ok = s.(*store).cfgMap.Load("test-config")
	assert.False(t, ok, "Associated Config should have been deleted from cfgMap")
}

func TestCreateOrUpdateConfig_WithUnexpectedGitConfigType(t *testing.T) {
	s := NewStore(10)

	// Store a MockGitConfig instead of *GitConfigEntry
	s.(*store).gitCfgMap.Store("test-gitconfig", &MockGitConfig{Name: "test-gitconfig"})

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	// Attempt to create Config with GitConfig of unexpected type
	err := s.CreateOrUpdateConfig(config)
	assert.ErrorIs(t, err, ErrGitConfigUnexpectedType, "Expected ErrGitConfigUnexpectedType when GitConfig has wrong type")
}

func TestCreateOrUpdateGitConfig_WithExistingConfigs(t *testing.T) {
	s := NewStore(10)

	// Create Configs that reference the GitConfig
	config1 := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "config1",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	config2 := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "config2",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	// Initially, GitConfig does not exist
	err := s.CreateOrUpdateConfig(config1)
	assert.ErrorIs(t, err, ErrGitConfigNotFound, "Expected ErrGitConfigNotFound when GitConfig does not exist")

	err = s.CreateOrUpdateConfig(config2)
	assert.ErrorIs(t, err, ErrGitConfigNotFound, "Expected ErrGitConfigNotFound when GitConfig does not exist")

	// Create GitConfig
	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	err = s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// After creating GitConfig, re-create Configs
	err = s.CreateOrUpdateConfig(config1)
	assert.NoError(t, err, "Creating Config1 should not produce an error")

	err = s.CreateOrUpdateConfig(config2)
	assert.NoError(t, err, "Creating Config2 should not produce an error")

	// Verify that both Configs are associated with the GitConfigEntry
	gitEntryInterface, ok := s.(*store).gitCfgMap.Load("test-gitconfig")
	assert.True(t, ok, "GitConfigEntry not found in gitCfgMap")

	gitEntry, ok := gitEntryInterface.(*GitConfigEntry)
	assert.True(t, ok, "Unexpected type stored in gitCfgMap")

	assert.Len(t, gitEntry.Configs, 2, "There should be two Configs associated with the GitConfigEntry")

	configNames := map[string]bool{
		"config1": false,
		"config2": false,
	}

	for _, cfg := range gitEntry.Configs {
		if _, exists := configNames[cfg.Cfg.Name]; exists {
			configNames[cfg.Cfg.Name] = true
		}
	}

	for name, found := range configNames {
		assert.True(t, found, "Config %s not associated with GitConfigEntry", name)
	}
}

func TestDeleteGitConfig_WithMultipleConfigs(t *testing.T) {
	s := NewStore(10)

	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	config1 := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "config1",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	config2 := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "config2",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	// Create GitConfig and Configs
	err := s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	err = s.CreateOrUpdateConfig(config1)
	assert.NoError(t, err, "Creating Config1 should not produce an error")

	err = s.CreateOrUpdateConfig(config2)
	assert.NoError(t, err, "Creating Config2 should not produce an error")

	// Delete GitConfig
	err = s.DeleteGitConfig("test-gitconfig")
	assert.NoError(t, err, "Deleting GitConfig should not produce an error")

	// Verify GitConfig is removed from gitCfgMap
	_, ok := s.(*store).gitCfgMap.Load("test-gitconfig")
	assert.False(t, ok, "GitConfigEntry should have been deleted from gitCfgMap")

	// Verify both Configs are removed from cfgMap
	_, ok = s.(*store).cfgMap.Load("config1")
	assert.False(t, ok, "Config1 should have been deleted from cfgMap")

	_, ok = s.(*store).cfgMap.Load("config2")
	assert.False(t, ok, "Config2 should have been deleted from cfgMap")
}

func TestConfigProducer(t *testing.T) {
	s := NewStore(10)
	configChan := s.ConfigProducer()
	assert.NotNil(t, configChan, "ConfigProducer returned nil")

	// Create GitConfig and Config
	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	// Create GitConfig
	err := s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// Create Config
	err = s.CreateOrUpdateConfig(config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Wait for the Buddle to be produced
	select {
	case buddle := <-configChan:
		assert.Equal(t, config, buddle.Config.Cfg, "Config in Buddle does not match the expected")
		assert.Equal(t, gitConfig, buddle.GitConfig, "GitConfig in Buddle does not match the expected")
	case <-time.After(time.Second * 2):
		t.Fatal("Timeout waiting for Buddle")
	}
}
