package device

type Device struct {
	IP       string
	Port     string
	Username string
	Password string
	Vendor   string
}

type DeviceInfo interface {
	GetHostName(config string, deviceType string) (string, error)
	Ping() (string, error)
	Type() string
	ShowCommand() (string, error)
}

type ExecResult struct {
	DeviceName string
	DeviceIP   string
	Output     string
	Error      error
}
