package api

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/hashicorp/go-cleanhttp"
	"github.com/hashicorp/hcl"
	"github.com/mitchellh/mapstructure"
)

const (
	// Default path at which SSH backend will be mounted in Vault server
	SSHHelperDefaultMountPoint = "ssh"

	// Echo request message sent as OTP by the vault-ssh-helper
	VerifyEchoRequest = "verify-echo-request"

	// Echo response message sent as a response to OTP matching echo request
	VerifyEchoResponse = "verify-echo-response"
)

// SSHHelper is a structure representing a vault-ssh-helper which can talk to vault server
// in order to verify the OTP entered by the user. It contains the path at which
// SSH backend is mounted at the server.
type SSHHelper struct {
	c          *Client
	MountPoint string
}

// SSHVerifyResponse is a structure representing the fields in Vault server's
// response.
type SSHVerifyResponse struct {
	// Usually empty. If the request OTP is echo request message, this will
	// be set to the corresponding echo response message.
	Message string `mapstructure:"message"`

	// Username associated with the OTP
	Username string `mapstructure:"username"`

	// IP associated with the OTP
	IP string `mapstructure:"ip"`
}

// SSHHelperConfig is a structure which represents the entries from the vault-ssh-helper's configuration file.
type SSHHelperConfig struct {
	VaultAddr       string `hcl:"vault_addr"`
	SSHMountPoint   string `hcl:"ssh_mount_point"`
	CACert          string `hcl:"ca_cert"`
	CAPath          string `hcl:"ca_path"`
	AllowedCidrList string `hcl:"allowed_cidr_list"`
	TLSSkipVerify   bool   `hcl:"tls_skip_verify"`
}

// TLSClient returns a HTTP client that uses TLS verification (TLS 1.2) for a given
// certificate pool.
func (c *SSHHelperConfig) SetTLSParameters(clientConfig *Config, certPool *x509.CertPool) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: c.TLSSkipVerify,
		MinVersion:         tls.VersionTLS12,
		RootCAs:            certPool,
	}

	transport := cleanhttp.DefaultTransport()
	transport.TLSClientConfig = tlsConfig
	clientConfig.HttpClient.Transport = transport
}

// NewClient returns a new client for the configuration. This client will be used by the
// vault-ssh-helper to communicate with Vault server and verify the OTP entered by user.
// If the configuration supplies Vault SSL certificates, then the client will
// have TLS configured in its transport.
func (c *SSHHelperConfig) NewClient() (*Client, error) {
	// Creating a default client configuration for communicating with vault server.
	clientConfig := DefaultConfig()

	// Pointing the client to the actual address of vault server.
	clientConfig.Address = c.VaultAddr

	// Check if certificates are provided via config file.
	if c.CACert != "" || c.CAPath != "" || c.TLSSkipVerify {
		var certPool *x509.CertPool
		var err error
		if c.CACert != "" {
			certPool, err = LoadCACert(c.CACert)
		} else if c.CAPath != "" {
			certPool, err = LoadCAPath(c.CAPath)
		}
		if err != nil {
			return nil, err
		}

		// Enable TLS on the HTTP client information
		c.SetTLSParameters(clientConfig, certPool)
	}

	// Creating the client object for the given configuration
	client, err := NewClient(clientConfig)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// LoadSSHHelperConfig loads ssh-helper's configuration from the file and populates the corresponding
// in-memory structure.
//
// Vault address is a required parameter.
// Mount point defaults to "ssh".
func LoadSSHHelperConfig(path string) (*SSHHelperConfig, error) {
	var config SSHHelperConfig
	contents, err := ioutil.ReadFile(path)
	if !os.IsNotExist(err) {
		obj, err := hcl.Parse(string(contents))
		if err != nil {
			return nil, err
		}

		if err := hcl.DecodeObject(&config, obj); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}

	if config.VaultAddr == "" {
		return nil, fmt.Errorf("config missing vault_addr")
	}
	if config.SSHMountPoint == "" {
		config.SSHMountPoint = SSHHelperDefaultMountPoint
	}

	return &config, nil
}

// SSHHelper creates an SSHHelper object which can talk to Vault server with SSH backend
// mounted at default path ("ssh").
func (c *Client) SSHHelper() *SSHHelper {
	return c.SSHHelperWithMountPoint(SSHHelperDefaultMountPoint)
}

// SSHHelperWithMountPoint creates an SSHHelper object which can talk to Vault server with SSH backend
// mounted at a specific mount point.
func (c *Client) SSHHelperWithMountPoint(mountPoint string) *SSHHelper {
	return &SSHHelper{
		c:          c,
		MountPoint: mountPoint,
	}
}

// Verify verifies if the key provided by user is present in Vault server. The response
// will contain the IP address and username associated with the OTP. In case the
// OTP matches the echo request message, instead of searching an entry for the OTP,
// an echo response message is returned. This feature is used by ssh-helper to verify if
// its configured correctly.
func (c *SSHHelper) Verify(otp string) (*SSHVerifyResponse, error) {
	data := map[string]interface{}{
		"otp": otp,
	}
	verifyPath := fmt.Sprintf("/v1/%s/verify", c.MountPoint)
	r := c.c.NewRequest("PUT", verifyPath)
	if err := r.SetJSONBody(data); err != nil {
		return nil, err
	}

	resp, err := c.c.RawRequest(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	secret, err := ParseSecret(resp.Body)
	if err != nil {
		return nil, err
	}

	if secret.Data == nil {
		return nil, nil
	}

	var verifyResp SSHVerifyResponse
	err = mapstructure.Decode(secret.Data, &verifyResp)
	if err != nil {
		return nil, err
	}
	return &verifyResp, nil
}
