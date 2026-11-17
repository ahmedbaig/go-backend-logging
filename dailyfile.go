package logging

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)


func NewDailyFile(o DailyFileOptions) (*DailyFile, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if !strings.Contains(o.Pattern, "%DATE%") {
		return nil, errors.New("pattern must contain %DATE%")
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, err
	}
	d := &DailyFile{opts: o}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.rotate(o.Now().Format(dateLayout)); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DailyFile) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if day := d.opts.Now().Format(dateLayout); day != d.day || d.f == nil {
		if err := d.rotate(day); err != nil {
			return 0, err
		}
	}
	return d.f.Write(p)
}

func (d *DailyFile) Close() error {
	d.mu.Lock()
	var err error
	if d.f != nil {
		err = d.f.Close()
		d.f = nil
	}
	d.mu.Unlock()
	d.wg.Wait()
	return err
}

func (d *DailyFile) path(day string) string {
	return filepath.Join(d.opts.Dir, strings.ReplaceAll(d.opts.Pattern, "%DATE%", day))
}

func (d *DailyFile) rotate(day string) error {
	prev := ""
	if d.f != nil {
		prev = d.f.Name()
		_ = d.f.Close()
	}
	f, err := os.OpenFile(d.path(day), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		d.f = nil
		return err
	}
	d.f, d.day = f, day

	if d.opts.Symlink != "" {
		link := filepath.Join(d.opts.Dir, d.opts.Symlink)
		_ = os.Remove(link)
		_ = os.Symlink(filepath.Base(f.Name()), link)
	}

	if prev != "" && prev != f.Name() {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			if d.opts.Compress {
				_ = gzipFile(prev)
			}
			d.prune()
		}()
	}
	return nil
}

func (d *DailyFile) prune() {
	if d.opts.MaxFiles <= 0 {
		return
	}
	prefix, suffix, _ := strings.Cut(d.opts.Pattern, "%DATE%")
	entries, err := os.ReadDir(d.opts.Dir)
	if err != nil {
		return
	}
	var matches []string
	for _, e := range entries {
		n := strings.TrimSuffix(e.Name(), ".gz")
		if e.Type().IsRegular() && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, suffix) {
			matches = append(matches, e.Name())
		}
	}
	sort.Strings(matches)
	for len(matches) > d.opts.MaxFiles {
		_ = os.Remove(filepath.Join(d.opts.Dir, matches[0]))
		matches = matches[1:]
	}
}

func gzipFile(path string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(path + ".gz")
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(path)
}
