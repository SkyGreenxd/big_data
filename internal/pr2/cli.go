package pr2

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"bigdata/pkg/webhdfs"
)

func flags(name string) *flag.FlagSet { return flag.NewFlagSet(name, flag.ContinueOnError) }
func parsed(f *flag.FlagSet, args []string) error {
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(f.Args(), " "))
	}
	return nil
}

type hdfsFlags struct{ endpoint, user, dataNode *string }

func hdfsOptions(f *flag.FlagSet) hdfsFlags {
	return hdfsFlags{f.String("hdfs", "http://localhost:9870", "WebHDFS URL"), f.String("user", "root", "HDFS user"), f.String("datanode", "http://localhost:9864", "single DataNode URL; empty uses advertised addresses")}
}
func (h hdfsFlags) client() (*webhdfs.Client, error) {
	return webhdfs.NewWithEndpoints(*h.endpoint, *h.dataNode, *h.user)
}

// Run executes a command using the supplied input and output streams.
func Run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pr2 <generate|upload|map|reduce|recommend|compare> [flags]")
	}
	switch args[0] {
	case "map", "reduce":
		f := flags(args[0])
		algorithm := f.String("algorithm", "pairs", "pairs or stripes")
		if err := parsed(f, args[1:]); err != nil {
			return err
		}
		if args[0] == "map" {
			return Map(*algorithm, in, out)
		}
		return Reduce(*algorithm, in, out)
	case "generate":
		f := flags("generate")
		c := GeneratorConfig{}
		f.IntVar(&c.Orders, "orders", 10000, "number of orders")
		f.IntVar(&c.Products, "products", 30, "catalog size")
		f.IntVar(&c.MinItems, "min-items", 1, "minimum basket size")
		f.IntVar(&c.MaxItems, "max-items", 8, "maximum basket size")
		f.Int64Var(&c.Seed, "seed", 42, "random seed")
		dir := f.String("out", "data", "local output directory (files must not exist)")
		if err := parsed(f, args[1:]); err != nil {
			return err
		}
		if err := c.Validate(); err != nil {
			return err
		}
		if err := os.MkdirAll(*dir, 0755); err != nil {
			return err
		}
		catalogPath := filepath.Join(*dir, "catalog.json")
		ordersPath := filepath.Join(*dir, "orders.jsonl")
		catalog, err := os.OpenFile(catalogPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		orders, err := os.OpenFile(ordersPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			catalog.Close()
			os.Remove(catalogPath)
			return err
		}
		generateErr := Generate(c, catalog, orders)
		catalogErr := catalog.Close()
		ordersErr := orders.Close()
		if err := errors.Join(generateErr, catalogErr, ordersErr); err != nil {
			os.Remove(catalogPath)
			os.Remove(ordersPath)
			return err
		}
		_, err = fmt.Fprintf(out, "Generated %d orders and %d products in %s\n", c.Orders, c.Products, *dir)
		return err
	case "upload":
		f := flags("upload")
		h := hdfsOptions(f)
		src := f.String("src", "data", "local data directory")
		dst := f.String("dst", "/shop/input", "HDFS input directory (files are overwritten)")
		if err := parsed(f, args[1:]); err != nil {
			return err
		}
		client, err := h.client()
		if err != nil {
			return err
		}
		if err := client.Mkdir(*dst); err != nil {
			return err
		}
		for _, name := range []string{"catalog.json", "orders.jsonl"} {
			file, err := os.Open(filepath.Join(*src, name))
			if err != nil {
				return err
			}
			uploadErr := client.Create(path.Join(*dst, name), file)
			closeErr := file.Close()
			if err := errors.Join(uploadErr, closeErr); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(out, "Uploaded catalog and orders to %s\n", *dst)
		return err
	case "recommend":
		f := flags("recommend")
		h := hdfsOptions(f)
		algorithm := f.String("algorithm", "pairs", "pairs or stripes")
		resultDir := f.String("results", "/shop/results/pairs", "HDFS result directory")
		catalogPath := f.String("catalog", "/shop/input/catalog.json", "HDFS catalog path")
		name := f.String("product", "", "product name; reads one line from stdin if omitted")
		limit := f.Int("limit", 10, "maximum recommendations")
		if err := parsed(f, args[1:]); err != nil {
			return err
		}
		if err := CheckAlgorithm(*algorithm); err != nil {
			return err
		}
		if *name == "" {
			if _, err := fmt.Fprintln(out, "Введите название товара:"); err != nil {
				return err
			}
			line, err := bufio.NewReader(in).ReadString('\n')
			if err != nil && !(errors.Is(err, io.EOF) && line != "") {
				return err
			}
			*name = strings.TrimSpace(line)
		}
		client, err := h.client()
		if err != nil {
			return err
		}
		reader, err := client.OpenReader(*catalogPath)
		if err != nil {
			return err
		}
		catalog, readErr := ReadCatalog(reader)
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		result, err := Recommend(client, *resultDir, *algorithm, catalog, *name, *limit)
		if err != nil {
			return err
		}
		if len(result) == 0 {
			_, err = fmt.Fprintln(out, "Совместные покупки не найдены.")
			return err
		}
		for i, r := range result {
			if _, err := fmt.Fprintf(out, "%d. %s\t%d\n", i+1, r.Product.Name, r.Count); err != nil {
				return err
			}
		}
		return nil
	case "compare":
		f := flags("compare")
		h := hdfsOptions(f)
		pairs := f.String("pairs", "/shop/results/pairs", "HDFS pairs output")
		stripes := f.String("stripes", "/shop/results/stripes", "HDFS stripes output")
		if err := parsed(f, args[1:]); err != nil {
			return err
		}
		client, err := h.client()
		if err != nil {
			return err
		}
		total, err := Compare(client, *pairs, *stripes)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Pairs and Stripes agree: %d directed pairs\n", total)
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
