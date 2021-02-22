package core

import (
	"encoding/json"
	"os"

	"github.com/tokentransfer/chain/account"

	libcore "github.com/tokentransfer/interfaces/core"
)

type Config struct {
	// address 0xc287B1266732495Fe8c93CE3Ba631597153fdd91
	// secret 86d3350f255e5b6259d3d3a615b363f23c042971d89b7f9cb84aa7fadeeb2736
	GasAddress string `json:"gas_address"`
	DataDir    string `json:"data_dir"`
	Address    string `json:"address"`
	Port       int64  `json:"port"`

	RPCAddress string `json:"rpc_address"`
	RPCPort    int64  `json:"rpc_port"`

	// address 0x768e56BCcb18a8622cc5BB5F6bfA6D82a255ab87
	// secret abb33a8c2bb48d3b1c2ce365685ac3b96563e6a17ebc4367cd637e33149f94ea
	Secret string `json:"secret"`

	Bootstraps []string `json:"bootstraps"`

	gasAccount libcore.Address
}

func NewConfig(configFile string) (*Config, error) {
	config := &Config{}

	cfg, err := os.Open(configFile)
	if err != nil {
		return nil, err
	}
	defer cfg.Close()

	jsonParser := json.NewDecoder(cfg)
	err = jsonParser.Decode(config)
	if err != nil {
		return nil, err
	}

	gasAccount := account.NewAddress()
	err = gasAccount.UnmarshalText([]byte(config.GasAddress))
	if err != nil {
		return nil, err
	}
	config.gasAccount = gasAccount
	return config, nil
}

func (c *Config) GetGasAccount() libcore.Address {
	return c.gasAccount
}

func (c *Config) GetDataDir() string {
	return c.DataDir
}

func (c *Config) GetAddress() string {
	return c.Address
}

func (c *Config) GetPort() int64 {
	return c.Port
}

func (c *Config) GetSecret() string {
	return c.Secret
}

func (c *Config) GetBootstraps() []string {
	return c.Bootstraps
}

func (c *Config) GetRPCAddress() string {
	return c.RPCAddress
}

func (c *Config) GetRPCPort() int64 {
	return c.RPCPort
}
