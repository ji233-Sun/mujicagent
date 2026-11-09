// Package markup shares image geometry and rendering between the editor and
// the numbered image sent to the model.
package markup

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func Load(path string) (*ui.Bitmap, int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return nil, info.Size(), "", fmt.Errorf("image preview exceeds 32 MiB or is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if err != nil {
		return nil, info.Size(), "", err
	}
	if len(data) > 32<<20 {
		return nil, info.Size(), "", fmt.Errorf("image preview exceeds 32 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, info.Size(), "", err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 24_000_000 {
		return nil, info.Size(), format, fmt.Errorf("image preview exceeds 24 megapixels")
	}
	bitmap, err := ui.DecodeBitmap(data)
	return bitmap, info.Size(), format, err
}

func Position(p model.ImagePoint, r ui.Rect) ui.Point {
	return ui.Point{X: r.X + float32(p.X/100)*r.W, Y: r.Y + float32(p.Y/100)*r.H}
}

func BadgePosition(p model.ImagePoint, r ui.Rect) ui.Point {
	q := Position(p, r)
	margin := min(float32(12), r.W/2, r.H/2)
	q.X = max(r.X+margin, min(q.X, r.X+r.W-margin))
	q.Y = max(r.Y+margin, min(q.Y, r.Y+r.H-margin))
	return q
}

// Paths includes arrowheads, so export and canvas hit testing share geometry.
func Paths(m model.Mark, r ui.Rect) [][][2]float32 {
	points := make([][2]float32, len(m.Points))
	for i, p := range m.Points {
		q := Position(p, r)
		points[i] = [2]float32{q.X, q.Y}
	}
	if len(points) < 2 {
		return nil
	}
	a, b := points[0], points[len(points)-1]
	switch m.Kind {
	case "rectangle":
		return [][][2]float32{{a, {b[0], a[1]}, b, {a[0], b[1]}, a}}
	case "arrow":
		angle := math.Atan2(float64(b[1]-a[1]), float64(b[0]-a[0]))
		length := float64(max(10, m.Width*4))
		left := [2]float32{b[0] - float32(math.Cos(angle-0.5)*length), b[1] - float32(math.Sin(angle-0.5)*length)}
		right := [2]float32{b[0] - float32(math.Cos(angle+0.5)*length), b[1] - float32(math.Sin(angle+0.5)*length)}
		return [][][2]float32{points, {left, b, right}}
	default:
		return [][][2]float32{points}
	}
}

func Draw(p *ui.Painter, notes []model.Annotation, r ui.Rect, selected string) {
	for i, n := range notes {
		for _, m := range n.Marks {
			color := ui.Hex(m.Color)
			paths := Paths(m, r)
			if n.ID == selected {
				for _, path := range paths {
					p.StrokePolyline(path, m.Width+7, ui.Hex("#ffffff"))
					p.StrokePolyline(path, m.Width+4, color.Alpha(.4))
				}
			}
			for _, path := range paths {
				p.StrokePolyline(path, m.Width, color)
			}
			if len(m.Points) == 0 {
				continue
			}
			q := BadgePosition(m.Points[0], r)
			// Keep number badges visible even at the image edges.
			badge := ui.Rect{X: q.X - 11, Y: q.Y - 11, W: 22, H: 22}
			if n.ID == selected {
				p.Fill(ui.Rect{X: q.X - 15, Y: q.Y - 15, W: 30, H: 30}, ui.Hex("#28413a"), 15)
			}
			p.Fill(badge, ui.Hex("#ffffff"), 11)
			p.Fill(ui.Rect{X: badge.X + 2, Y: badge.Y + 2, W: 18, H: 18}, color, 9)
			label := fmt.Sprint(i + 1)
			p.Text(q.X-float32(len(label))*3.1, q.Y-7, label, 11, ui.Hex("#ffffff"))
		}
	}
}

// Export writes an immutable derived image in the application data directory.
// It runs in the existing send worker; original files are never modified.
func Export(a model.Attachment, dir string) (string, error) {
	bitmap, _, _, err := Load(a.Path)
	if err != nil {
		return "", err
	}
	w, h := bitmap.Size()
	scale := min(1, float64(2000)/float64(max(w, h)))
	w, h = max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	img := ui.Render(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, ui.Hex("#ffffff"), 0)
			p.Image(bitmap, r, ui.Contain)
			Draw(p, a.Annotations, r, "")
		})
	}, w, h, 1)
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "attachments", fmt.Sprintf("%x", sha256.Sum256(data.Bytes())), "annotated.png")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		return "", err
	}
	return path, nil
}
