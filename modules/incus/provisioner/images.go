package main

import (
	"context"
	"fmt"
	"net/url"
)

type imageRecord struct {
	Fingerprint  string `json:"fingerprint"`
	Architecture string `json:"architecture"`
	Type         string `json:"type"`
}

func verifyImages(ctx context.Context, c *client, l lease) error {
	architecture := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[l.ImageArchitecture]
	if architecture == "" {
		return fmt.Errorf("image architecture must be amd64 or arm64")
	}
	imageType := map[string]string{"container": "container", "vm": "virtual-machine"}[l.Isolation]
	if imageType == "" {
		return fmt.Errorf("unsupported image isolation")
	}
	for _, pin := range l.ImageAllowlist {
		var image imageRecord
		if err := c.do(ctx, "GET", "/1.0/images/"+pin+"?project="+url.QueryEscape(l.Sandbox), nil, &image); err != nil {
			return fmt.Errorf("compute image %s is unavailable in project %s; import the identical artifact before apply: %w", pin, l.Sandbox, err)
		}
		if image.Fingerprint != pin || image.Architecture != architecture || image.Type != imageType {
			return fmt.Errorf("compute image %s does not match its frozen fingerprint, architecture or isolation", pin)
		}
	}
	return nil
}
