package config

// CLIConfig holds parsed command line flags and configuration overrides
type CLIConfig struct {
	Port         string
	Debug        bool
	Bind         string
	NoJanitor    bool
	SimpleHealth bool
	ConfigCheck  bool
	Version      bool
	Help         bool
}
