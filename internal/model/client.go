package model

import "context"

type Client struct{}

func (c *Client) Chat(context.Context) error { return nil }
