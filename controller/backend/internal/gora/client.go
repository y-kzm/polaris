// Package gora provides a gRPC client for go-ra agents.
// Each agent runs on a router and manages Router Advertisement (RA) settings
// for the router's network interfaces.
package gora

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	gorav1 "controller/backend/api/gora/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DefaultPort is the gRPC port that go-ra agents listen on.
const DefaultPort = 50051

type Client struct {
	addr string
	conn *grpc.ClientConn
	svc  gorav1.GoRAServiceClient
}

func New(address string, port int) (*Client, error) {
	target := "passthrough:///" + formatTarget(address, port)
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", target, err)
	}
	return &Client{addr: target, conn: conn, svc: gorav1.NewGoRAServiceClient(conn)}, nil
}

func (c *Client) Close() { c.conn.Close() }

func (c *Client) GetStatus(ctx context.Context) ([]*gorav1.InterfaceStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.svc.GetStatus(ctx, &gorav1.GetStatusRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Interfaces, nil
}

func (c *Client) ListInterfaces(ctx context.Context) ([]*gorav1.InterfaceConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.svc.ListInterfaces(ctx, &gorav1.ListInterfacesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Interfaces, nil
}

func (c *Client) AddInterface(ctx context.Context, cfg *gorav1.InterfaceConfig) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.svc.AddInterface(ctx, &gorav1.AddInterfaceRequest{Interface: cfg})
	return err
}

func (c *Client) UpdateInterface(ctx context.Context, cfg *gorav1.InterfaceConfig) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.svc.UpdateInterface(ctx, &gorav1.UpdateInterfaceRequest{Interface: cfg})
	return err
}

func (c *Client) DeleteInterface(ctx context.Context, id int32) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.svc.DeleteInterface(ctx, &gorav1.DeleteInterfaceRequest{Id: id})
	return err
}

// Upsert calls UpdateInterface; if that fails, falls back to AddInterface.
func (c *Client) Upsert(ctx context.Context, cfg *gorav1.InterfaceConfig) error {
	if err := c.UpdateInterface(ctx, cfg); err == nil {
		return nil
	}
	return c.AddInterface(ctx, cfg)
}

func formatTarget(address string, port int) string {
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return net.JoinHostPort(host, strconv.Itoa(port))
}
