package main

import (
	"testing"

	"bilidown/bilibili"
	"bilidown/common"
)

func TestParseBVID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"BV1xx411c7mD", "BV1xx411c7mD"},
		{"https://www.bilibili.com/video/BV1xx411c7mD", "BV1xx411c7mD"},
		{"https://www.bilibili.com/video/BV1xx411c7mD?p=1", "BV1xx411c7mD"},
		{"https://b23.tv/BV1xx411c7mD", "BV1xx411c7mD"},
		{"invalid", ""},
		{"", ""},
		{"BV1GJ411x7h7", "BV1GJ411x7h7"},
		{"https://www.bilibili.com/video/BV1GJ411x7h7/", "BV1GJ411x7h7"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseBVID(tt.input)
			if result != tt.expected {
				t.Errorf("parseBVID(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		seconds  int
		expected string
	}{
		{0, "0:00"},
		{59, "0:59"},
		{60, "1:00"},
		{61, "1:01"},
		{3599, "59:59"},
		{3600, "1:00:00"},
		{3661, "1:01:01"},
		{86400, "24:00:00"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatDuration(tt.seconds)
			if result != tt.expected {
				t.Errorf("formatDuration(%d) = %q, want %q", tt.seconds, result, tt.expected)
			}
		})
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := humanBytes(tt.bytes)
			if result != tt.expected {
				t.Errorf("humanBytes(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

func TestFormatQualityName(t *testing.T) {
	tests := []struct {
		format   common.MediaFormat
		contains string
	}{
		{127, "8K"},
		{120, "4K"},
		{80, "1080P"},
		{64, "720P"},
		{32, "480P"},
		{16, "360P"},
		{999, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.contains, func(t *testing.T) {
			result := formatQualityName(tt.format)
			if len(result) == 0 {
				t.Errorf("formatQualityName(%d) returned empty string", tt.format)
			}
		})
	}
}

func TestSelectBestFormat(t *testing.T) {
	// Test with AcceptQuality
	playInfo := &bilibili.PlayInfo{
		AcceptQuality: []common.MediaFormat{120, 80, 64},
	}
	result := selectBestFormat(playInfo)
	if result != 120 {
		t.Errorf("selectBestFormat with AcceptQuality = %d, want 120", result)
	}

	// Test with Dash video only
	playInfo2 := &bilibili.PlayInfo{
		Dash: &bilibili.Dash{
			Video: []bilibili.Media{
				{ID: 80},
				{ID: 64},
			},
		},
	}
	result2 := selectBestFormat(playInfo2)
	if result2 != 80 {
		t.Errorf("selectBestFormat with Dash.Video = %d, want 80", result2)
	}

	// Test empty
	playInfo3 := &bilibili.PlayInfo{}
	result3 := selectBestFormat(playInfo3)
	if result3 != 80 {
		t.Errorf("selectBestFormat empty = %d, want 80", result3)
	}
}

func TestRoundFloat(t *testing.T) {
	tests := []struct {
		val       float64
		precision int
		expected  float64
	}{
		{1.234, 1, 1.2},
		{1.256, 1, 1.3},
		{1.0, 2, 1.0},
		{99.999, 0, 100.0},
	}

	for _, tt := range tests {
		result := roundFloat(tt.val, tt.precision)
		if result != tt.expected {
			t.Errorf("roundFloat(%f, %d) = %f, want %f", tt.val, tt.precision, result, tt.expected)
		}
	}
}
