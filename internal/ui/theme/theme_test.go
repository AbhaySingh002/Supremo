package theme

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"testing"
)

func TestDefaultHonorsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	design := Default()
	if !design.NoColor {
		t.Fatal("NO_COLOR should select the text-only theme")
	}
	if rendered := design.Card.Render("plain"); strings.Contains(rendered, "\x1b[") {
		t.Fatalf("NO_COLOR emitted ANSI styling: %q", rendered)
	}
}

func TestInkSignalPaletteAndContrast(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	design := Default()
	want := map[string]string{
		"canvas": "#090B0F", "surface": "#11151B", "elevated": "#171C24",
		"text": "#E8EAF0", "focus": "#E8B84A", "user": "#70ADEC",
		"read": "#66B8D4", "write": "#E29A61", "search": "#A99BE0",
		"success": "#70C991", "error": "#E27682",
	}
	got := map[string]color.Color{
		"canvas": design.Background, "surface": design.Surface, "elevated": design.ElevatedBG,
		"text": design.Primary, "focus": design.Accent, "user": design.User,
		"read": design.ToolRead, "write": design.ToolWrite, "search": design.ToolSearch,
		"success": design.Success, "error": design.Error,
	}
	for name, expected := range want {
		if actual := colorHex(got[name]); actual != expected {
			t.Errorf("%s = %s, want %s", name, actual, expected)
		}
	}
	foregrounds := map[string]color.Color{
		"text": design.Primary, "muted": design.Secondary, "dim": design.TextDim,
		"focus": design.Accent, "user": design.User, "read": design.ToolRead,
		"write": design.ToolWrite, "search": design.ToolSearch, "success": design.Success, "error": design.Error,
	}
	for name, foreground := range foregrounds {
		for backgroundName, background := range map[string]color.Color{"canvas": design.Background, "surface": design.Surface, "elevated": design.ElevatedBG} {
			if ratio := contrastRatio(foreground, background); ratio < 4.5 {
				t.Errorf("%s on %s contrast = %.2f, want >= 4.5", name, backgroundName, ratio)
			}
		}
	}
}

func colorHex(value color.Color) string {
	r, g, b, _ := value.RGBA()
	return fmt.Sprintf("#%02X%02X%02X", r>>8, g>>8, b>>8)
}

func contrastRatio(a, b color.Color) float64 {
	x, y := relativeLuminance(a), relativeLuminance(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}

func relativeLuminance(value color.Color) float64 {
	r, g, b, _ := value.RGBA()
	linear := func(channel uint32) float64 {
		v := float64(channel) / 65535
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}
