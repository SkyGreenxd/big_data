package pr2

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/SkyGreenxd/bigdata/pkg/webhdfs"
)

type FileSystem interface {
	List(string) ([]webhdfs.FileStatus, error)
	OpenReader(string) (io.ReadCloser, error)
}

type Recommendation struct {
	Product Product
	Count   int64
}

func ReadCatalog(in io.Reader) ([]Product, error) {
	var products []Product
	dec := json.NewDecoder(in)
	if err := dec.Decode(&products); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("catalog: expected a single JSON array")
	}
	if len(products) == 0 {
		return nil, fmt.Errorf("catalog is empty")
	}
	seen := make(map[string]bool)
	for _, p := range products {
		if !validID(p.ID) || strings.TrimSpace(p.Name) == "" || seen[p.ID] {
			return nil, fmt.Errorf("invalid or duplicate catalog product %q", p.ID)
		}
		seen[p.ID] = true
	}
	return products, nil
}

func Resolve(products []Product, name string) (Product, error) {
	var matches []Product
	for _, p := range products {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(p.Name)) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		return Product{}, fmt.Errorf("unknown product %q", name)
	}
	if len(matches) > 1 {
		return Product{}, fmt.Errorf("ambiguous product name %q", name)
	}
	return matches[0], nil
}

// WalkResults opens every reducer part directly through WebHDFS, one file at a time.
func WalkResults(fs FileSystem, dir, algorithm string, consume func(Record) error) error {
	if err := CheckAlgorithm(algorithm); err != nil {
		return err
	}
	files, err := fs.List(dir)
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].PathSuffix < files[j].PathSuffix })
	complete := false
	parts := 0
	for _, file := range files {
		if file.PathSuffix == "_SUCCESS" && file.Type == "FILE" {
			complete = true
		}
	}
	if !complete {
		return fmt.Errorf("%s: missing _SUCCESS; job has not completed", dir)
	}
	for _, file := range files {
		if !strings.HasPrefix(file.PathSuffix, "part-") || file.Type != "FILE" {
			continue
		}
		if strings.ContainsAny(file.PathSuffix, "/\\") {
			return fmt.Errorf("invalid part filename")
		}
		parts++
		filename := path.Join(dir, file.PathSuffix)
		r, err := fs.OpenReader(filename)
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		parseErr := Lines(r, func(line string) error {
			rec, err := ParseRecord(algorithm, line)
			if err != nil {
				return err
			}
			return consume(rec)
		})
		closeErr := r.Close()
		if parseErr != nil {
			return fmt.Errorf("%s: %w", filename, parseErr)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if parts == 0 {
		return fmt.Errorf("%s: no part-* files", dir)
	}
	return nil
}

func Recommend(fs FileSystem, dir, algorithm string, products []Product, name string, limit int) ([]Recommendation, error) {
	if limit < 1 {
		return nil, fmt.Errorf("limit must be positive")
	}
	target, err := Resolve(products, name)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64)
	err = WalkResults(fs, dir, algorithm, func(rec Record) error {
		if algorithm == AlgorithmPairs {
			if rec.Pair[0] != target.ID {
				return nil
			}
			next, err := Add(counts[rec.Pair[1]], rec.Count)
			counts[rec.Pair[1]] = next
			return err
		}
		if rec.Key != target.ID {
			return nil
		}
		for id, count := range rec.Counts {
			next, err := Add(counts[id], count)
			if err != nil {
				return err
			}
			counts[id] = next
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	index := make(map[string]Product, len(products))
	for _, p := range products {
		index[p.ID] = p
	}
	result := make([]Recommendation, 0, len(counts))
	for id, count := range counts {
		p, ok := index[id]
		if !ok {
			return nil, fmt.Errorf("result references unknown product ID %q", id)
		}
		result = append(result, Recommendation{Product: p, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		if result[i].Product.Name != result[j].Product.Name {
			return result[i].Product.Name < result[j].Product.Name
		}
		return result[i].Product.ID < result[j].Product.ID
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// Compare is a validation utility; its full pair matrix is intended for demo-sized data.
func Compare(fs FileSystem, pairsDir, stripesDir string) (int, error) {
	expected := make(map[Pair]int64)
	err := WalkResults(fs, pairsDir, AlgorithmPairs, func(rec Record) error {
		if _, exists := expected[rec.Pair]; exists {
			return fmt.Errorf("duplicate pair %v", rec.Pair)
		}
		expected[rec.Pair] = rec.Count
		return nil
	})
	if err != nil {
		return 0, err
	}
	total := len(expected)
	seen := make(map[string]bool)
	err = WalkResults(fs, stripesDir, AlgorithmStripes, func(rec Record) error {
		if seen[rec.Key] {
			return fmt.Errorf("duplicate stripe %q", rec.Key)
		}
		seen[rec.Key] = true
		for id, count := range rec.Counts {
			pair := Pair{rec.Key, id}
			if expected[pair] != count {
				return fmt.Errorf("pair %v: pairs=%d, stripes=%d", pair, expected[pair], count)
			}
			delete(expected, pair)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if len(expected) > 0 {
		return 0, fmt.Errorf("stripes is missing %d pairs", len(expected))
	}
	return total, nil
}
