package store

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

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
	err := s.CreateOrUpdateGitConfig(context.Background(), gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// Create or update Config
	err = s.CreateOrUpdateConfig(context.Background(), config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Verify if the config was stored correctly in cfgMap
	s.(*store).mu.RLock()
	c, ok := s.(*store).cfgMap["test-config"]
	s.(*store).mu.RUnlock()
	assert.True(t, ok, "Config not found in cfgMap")
	assert.Equal(t, config, c.Cfg, "Stored Config does not match the original")

	// Verify association with GitConfigEntry in gitCfgMap
	s.(*store).mu.RLock()
	gitEntry, ok := s.(*store).gitCfgMap["test-gitconfig"]
	s.(*store).mu.RUnlock()
	assert.True(t, ok, "GitConfigEntry not found in gitCfgMap")

	assert.Equal(t, gitConfig, gitEntry.GitConfig, "Stored GitConfig does not match the original")

	// Verify that the Config is associated with the GitConfigEntry
	gitEntry.mu.Lock()
	_, found := gitEntry.Configs["test-config"]
	gitEntry.mu.Unlock()
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
	err := s.CreateOrUpdateGitConfig(context.Background(), gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// Verify if the GitConfig was stored correctly in gitCfgMap
	s.(*store).mu.RLock()
	storedGitConfigEntry, ok := s.(*store).gitCfgMap["test-gitconfig"]
	s.(*store).mu.RUnlock()
	assert.True(t, ok, "GitConfigEntry not found in gitCfgMap")

	assert.Equal(t, gitConfig, storedGitConfigEntry.GitConfig, "Stored GitConfig does not match the original")

	// Verify that the Configs map is initially empty
	storedGitConfigEntry.mu.Lock()
	assert.Empty(t, storedGitConfigEntry.Configs, "Configs map should be empty initially")
	storedGitConfigEntry.mu.Unlock()
}

func TestBundleProducedOnConfigCreation(t *testing.T) {
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
	err := s.CreateOrUpdateGitConfig(context.Background(), gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// Create or update Config
	err = s.CreateOrUpdateConfig(context.Background(), config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Wait for the Bundle to be produced
	select {
	case bundleFunc := <-s.ConfigProducer():
		bundle, err := bundleFunc()
		assert.NoError(t, err, "Error getting Bundle")
		assert.Equal(t, config, bundle.Config.Cfg, "Config in Bundle does not match the expected")
		assert.Equal(t, gitConfig, bundle.Git.GitConfig, "GitConfig in Bundle does not match the expected")
	case <-time.After(time.Second * 2):
		t.Fatal("Timeout waiting for Bundle")
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
	err := s.CreateOrUpdateConfig(context.Background(), config)
	assert.ErrorIs(t, err, ErrGitConfigNotFound, "Expected ErrGitConfigNotFound when GitConfig does not exist")
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
	err := s.CreateOrUpdateGitConfig(context.Background(), gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	err = s.CreateOrUpdateConfig(context.Background(), config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Delete Config
	err = s.DeleteConfig(context.Background(), "test-config")
	assert.NoError(t, err, "Deleting Config should not produce an error")

	// Verify Config is removed from cfgMap
	s.(*store).mu.RLock()
	_, ok := s.(*store).cfgMap["test-config"]
	s.(*store).mu.RUnlock()
	assert.False(t, ok, "Config should have been deleted from cfgMap")

	// Verify Config is removed from GitConfigEntry
	s.(*store).mu.RLock()
	gitEntry, ok := s.(*store).gitCfgMap["test-gitconfig"]
	s.(*store).mu.RUnlock()
	assert.True(t, ok, "GitConfigEntry not found in gitCfgMap")

	gitEntry.mu.Lock()
	_, found := gitEntry.Configs["test-config"]
	gitEntry.mu.Unlock()
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
	err := s.CreateOrUpdateGitConfig(context.Background(), gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	err = s.CreateOrUpdateConfig(context.Background(), config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Delete GitConfig
	err = s.DeleteGitConfig(context.Background(), "test-gitconfig")
	assert.NoError(t, err, "Deleting GitConfig should not produce an error")

	// Verify GitConfig is removed from gitCfgMap
	s.(*store).mu.RLock()
	_, ok := s.(*store).gitCfgMap["test-gitconfig"]
	s.(*store).mu.RUnlock()
	assert.False(t, ok, "GitConfigEntry should have been deleted from gitCfgMap")

	// Verify associated Config is also deleted from cfgMap
	s.(*store).mu.RLock()
	_, ok = s.(*store).cfgMap["test-config"]
	s.(*store).mu.RUnlock()
	assert.False(t, ok, "Associated Config should have been deleted from cfgMap")
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
	err := s.CreateOrUpdateGitConfig(context.Background(), gitConfig)
	assert.NoError(t, err, "Creating GitConfig should not produce an error")

	// Create Config
	err = s.CreateOrUpdateConfig(context.Background(), config)
	assert.NoError(t, err, "Creating Config should not produce an error")

	// Wait for the Bundle to be produced
	select {
	case bundleFunc := <-configChan:
		bundle, err := bundleFunc()
		assert.NoError(t, err, "Error getting Bundle")
		assert.Equal(t, config, bundle.Config.Cfg, "Config in Bundle does not match the expected")
		assert.Equal(t, gitConfig, bundle.Git.GitConfig, "GitConfig in Bundle does not match the expected")
	case <-time.After(time.Second * 2):
		t.Fatal("Timeout waiting for Bundle")
	}
}
