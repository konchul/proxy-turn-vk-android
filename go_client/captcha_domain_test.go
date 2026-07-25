package main

import "testing"

func TestCaptchaDomainFromRedirectURI(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "domain query",
			uri:  "https://id.vk.ru/captcha?session_token=abc&domain=vk.ru",
			want: "vk.ru",
		},
		{
			name: "missing domain falls back",
			uri:  "https://id.vk.ru/captcha?session_token=abc",
			want: "vk.com",
		},
		{
			name: "invalid uri falls back",
			uri:  "://bad",
			want: "vk.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := captchaDomainFromRedirectURI(tc.uri)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
