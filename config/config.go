package config

import (
	"fmt"
	"os"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Git      GitConfig      `yaml:"git"`
	Registry RegistryConfig `yaml:"registry"`
	Services []ServiceConfig `yaml:"services"`
	Database DatabaseConfig `yaml:"database"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	Port    int      `yaml:"port"`
	APIKeys []APIKey `yaml:"api_keys"`
}

type APIKey struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

type GitConfig struct {
	RepositoryURL string `yaml:"repository_url"`
	Branch        string `yaml:"branch"`
	Username      string `yaml:"username"`
	Token         string `yaml:"token"`
	LocalPath     string `yaml:"local_path"`
	AuthorName    string `yaml:"author_name"`
	AuthorEmail   string `yaml:"author_email"`
}

type RegistryConfig struct {
	Type               string `yaml:"type"`               // "docker", "ecr"
	Region             string `yaml:"region"`             // AWS region for ECR
	AccountID          string `yaml:"account_id"`         // AWS account ID for ECR
	AccessKeyID        string `yaml:"access_key_id"`      // AWS access key ID
	SecretAccessKey    string `yaml:"secret_access_key"`  // AWS secret access key
	ImagePullSecretName string `yaml:"image_pull_secret_name"` // K8s secret name for image pulls
}

type ServiceConfig struct {
	Name            string       `yaml:"name"`
	Namespace       string       `yaml:"namespace"`
	ManifestPath    string       `yaml:"manifest_path"`
	ImageRepository string       `yaml:"image_repository"`
	WorkloadType    string       `yaml:"workload_type"` // deployment, statefulset, cronjob
	RegistryAuth    *RegistryAuth `yaml:"registry_auth,omitempty"`
}

type RegistryAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Expand environment variables
	dataStr := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(dataStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Set defaults
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Git.Branch == "" {
		cfg.Git.Branch = "main"
	}
	if cfg.Git.LocalPath == "" {
		cfg.Git.LocalPath = "/data/gitops-repo"
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = "/data/deployments.db"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}

	// Set registry defaults
	if cfg.Registry.Type == "" {
		cfg.Registry.Type = "docker"
	}
	if cfg.Registry.ImagePullSecretName == "" {
		cfg.Registry.ImagePullSecretName = "registry-credentials"
	}

	// Set default workload types
	for i := range cfg.Services {
		if cfg.Services[i].WorkloadType == "" {
			cfg.Services[i].WorkloadType = "deployment"
		}
	}

	return &cfg, nil
}

func (c *Config) GetService(name string) *ServiceConfig {
	for _, svc := range c.Services {
		if svc.Name == name {
			return &svc
		}
	}
	return nil
}

func (c *Config) ValidateAPIKey(key string) bool {
	for _, ak := range c.Server.APIKeys {
		if ak.Key == key {
			return true
		}
	}
	return false
}
