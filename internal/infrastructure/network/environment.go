package network

import (
	"context"
	"net"
)

type Environment struct{}

func (Environment) LocalAddresses(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	result := []string{"0.0.0.0", "::"}
	for _, address := range addresses {
		if ip, _, err := net.ParseCIDR(address.String()); err == nil {
			result = append(result, ip.String())
		}
	}
	return result, nil
}
