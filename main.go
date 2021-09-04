package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cheggaaa/pb/v3"
	"github.com/go-ping/ping"
	"github.com/urfave/cli/v2"
	"golang.org/x/sync/errgroup"
)

func main() {
	if err := _main(); err != nil {
		log.Fatal(err)
	}
}

func _main() error {
	app := cli.NewApp()
	app.Name = "solana-latency-checker"
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:  "url",
			Usage: "url",
			Value: "mainnet-beta",
		},
		&cli.UintFlag{
			Name:  "top",
			Usage: "top",
			Value: 10,
		},
	}
	app.Action = func(c *cli.Context) error {
		url, err := buildURL(c.String("url"))
		if err != nil {
			return err
		}
		nodes, err := getClusterNodes(url)
		if err != nil {
			return err
		}
		latency, err := checkLatency(nodes)
		if err != nil {
			return err
		}
		printResult(latency, c.Uint("top"))
		return nil
	}
	return app.Run(os.Args)
}

type ClusterNodes struct {
	Result []ClusterNode `json:"result"`
}

type ClusterLatency struct {
	Time        time.Duration
	IP          string
	ClusterNode *ClusterNode
}

type ClusterNode struct {
	FeatureSet   uint   `json:"featureSet"`
	Gossip       string `json:"gossip"`
	Pubkey       string `json:"pubkey"`
	Rpc          string `json:"rpc"`
	ShredVersion uint   `json:"shredVersion"`
	Tpu          string `json:"tpu"`
	Version      string `json:"version"`
}

func buildURL(urlKey string) (string, error) {
	u1, err := url.Parse(urlKey)
	if err != nil {
		return "", err
	}
	if u1.Scheme == "" || u1.Host == "" {
		u2, err := url.Parse(fmt.Sprintf("http://api.%s.solana.com", urlKey))
		if err != nil {
			return "", err
		}
		return u2.String(), nil
	}
	return u1.String(), nil
}

func getClusterNodes(url string) (*ClusterNodes, error) {
	resp, err := http.Post(url, "application/json", bytes.NewBuffer([]byte(`{"jsonrpc":"2.0","id":1, "method":"getClusterNodes"}`)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	data := ClusterNodes{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	return &data, nil
}

const concurrency = 20

func checkLatency(clusterNodes *ClusterNodes) ([]ClusterLatency, error) {
	result := []ClusterLatency{}
	sem := make(chan struct{}, concurrency)
	eg := errgroup.Group{}
	bar := pb.Simple.Start(len(clusterNodes.Result))
	bar.SetMaxWidth(80)
	for _, node := range clusterNodes.Result {
		sem <- struct{}{}
		node := node
		eg.Go(func() error {
			defer func() { <-sem }()
			split := strings.Split(node.Gossip, ":")
			if len(split) != 2 {
				return errors.New("invalid gossip ip")
			}
			pinger, err := ping.NewPinger(split[0])
			if err != nil {
				return err
			}
			pinger.Count = 5
			pinger.Interval = 200 * time.Millisecond
			pinger.Timeout = 3 * time.Second

			if err := pinger.Run(); err != nil {
				return err
			}
			pingResult := pinger.Statistics()
			if pingResult.PacketLoss == 0 {
				result = append(result, ClusterLatency{Time: pingResult.AvgRtt, IP: split[0], ClusterNode: &node})
			}
			bar.Increment()
			return nil
		})
	}
	bar.Finish()
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

func printResult(latency []ClusterLatency, top uint) {
	sort.Slice(latency, func(i, j int) bool { return latency[i].Time < latency[j].Time })
	count := int(top)
	if len(latency) < 10 {
		count = len(latency)
	}
	for i := 0; i < count; i++ {
		fmt.Printf("[%d] time:%dms, ip:%s, pubkey:%s\n", i, latency[i].Time.Milliseconds(), latency[i].IP, latency[i].ClusterNode.Pubkey)
	}
}
