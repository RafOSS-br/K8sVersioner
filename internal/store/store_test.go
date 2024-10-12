package store

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersionerls/api/v1alpha1"
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

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	err := s.CreateOrUpdateConfig(config)
	assert.NoError(t, err)

	// Verify if the config was stored correctly
	c, ok := s.(*store).cfgMap.Load("test-config")
	assert.True(t, ok, "Config not found in the map")

	storedConfig, ok := c.(*Config)
	assert.True(t, ok, "Unexpected type stored in the map")
	assert.Equal(t, config, storedConfig.Cfg, "Stored config does not match the original")
}

func TestCreateOrUpdateGitConfig(t *testing.T) {
	s := NewStore(10)

	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	err := s.CreateOrUpdateGitConfig(gitConfig)
	assert.NoError(t, err)

	// Verify if the gitConfig was stored correctly
	gc, ok := s.(*store).gitCfgMap.Load("test-gitconfig")
	assert.True(t, ok, "GitConfig not found in the map")

	storedGitConfig, ok := gc.(*k8sversionerv1alpha1.GitConfig)
	assert.True(t, ok, "Unexpected type stored in the map")
	assert.Equal(t, gitConfig, storedGitConfig, "Stored GitConfig does not match the original")
}

func TestSubmitConfig(t *testing.T) {
	s := NewStore(10)

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	gitConfig := &k8sversionerv1alpha1.GitConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-gitconfig",
		},
	}

	// Store the Config and the GitConfig
	s.CreateOrUpdateConfig(config)
	s.CreateOrUpdateGitConfig(gitConfig)

	err := s.SubmitConfig("test-config")
	assert.NoError(t, err)
	select {
	case buddle := <-s.ConfigProducer():
		assert.Equal(t, config, buddle.Config.Cfg, "Config in Buddle does not match the expected")
		assert.Equal(t, gitConfig, buddle.GitConfig, "GitConfig in Buddle does not match the expected")
	case <-time.After(time.Second * 2):
		t.Fatal("Timeout waiting for Buddle")
	}
}

func TestSubmitConfig_ConfigNotFound(t *testing.T) {
	s := NewStore(10)

	err := s.SubmitConfig("nonexistent-config")
	assert.ErrorIs(t, err, ErrConfigNotFound)
}

func TestSubmitConfig_GitConfigNotFound(t *testing.T) {
	s := NewStore(10)

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "nonexistent-gitconfig",
		},
	}

	s.CreateOrUpdateConfig(config)

	err := s.SubmitConfig("test-config")
	assert.ErrorIs(t, err, ErrGitConfigNotFound)
}

func TestSubmitConfig_UnexpectedType(t *testing.T) {
	s := NewStore(10)

	s.(*store).cfgMap.Store("test-config", &MockConfig{Name: "test-config"})

	err := s.SubmitConfig("test-config")
	assert.ErrorIs(t, err, ErrConfigUnexpectedType)
}

func TestSubmitConfig_GitConfigUnexpectedType(t *testing.T) {
	s := NewStore(10)

	config := &k8sversionerv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-config",
		},
		Spec: k8sversionerv1alpha1.ConfigSpec{
			GitRef: "test-gitconfig",
		},
	}

	s.CreateOrUpdateConfig(config)
	s.(*store).gitCfgMap.Store("test-gitconfig", &MockGitConfig{Name: "test-gitconfig"})

	err := s.SubmitConfig("test-config")
	assert.ErrorIs(t, err, ErrGitConfigUnexpectedType)
}

func TestConfigProducer(t *testing.T) {
	s := NewStore(10)
	configChan := s.ConfigProducer()
	assert.NotNil(t, configChan, "ConfigProducer returned nil")
}
