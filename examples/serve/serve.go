package main

// This example launches an IPFS-Lite peer and fetches a hello-world
// hash from the IPFS network.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multiformats/go-base32"
	mh "github.com/multiformats/go-multihash"

	ipfslite "github.com/hsanjuan/ipfs-lite"
	"github.com/ipfs/go-cid"

	crypto "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/multiformats/go-multiaddr"

	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
)

func main() {
	err := mainRet()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func mainRet() error {
	dbPath := "data/db.json"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// dssync.MutexWrap

	datastore := NewSkynetDatastore()
	buf, err := ioutil.ReadFile(dbPath)
	if err != nil {
		return err
	}

	var m map[string]string

	// Unmarshal or Decode the JSON to the interface.
	err = json.Unmarshal(buf, &m)
	if err != nil {
		return err
	}

	for k, v := range m {
		if strings.HasPrefix(v, "sia://") {
			datastore.skynetMap[ds.NewKey("/blocks/"+k)] = v
		} else {
			datastore.values[ds.NewKey("/blocks/"+k)], _ = hex.DecodeString(v)
		}

	}

	// fmt.Println("ds.skynetMap", datastore.skynetMap)
	// fmt.Println("ds.values", datastore.values)

	// priv, _, err := crypto.GenerateKeyPair(crypto.RSA, 2048)
	r, err := os.Open("key.txt")
	if err != nil {
		return err
	}

	priv, _, err := crypto.GenerateEd25519Key(r)
	if err != nil {
		return err
	}

	listen, err := multiaddr.NewMultiaddr("/ip4/0.0.0.0/tcp/4005")
	if err != nil {
		return err
	}

	h, dht, err := ipfslite.SetupLibp2p(
		ctx,
		priv,
		nil,
		[]multiaddr.Multiaddr{listen},
		datastore,
		ipfslite.Libp2pOptionsExtra...,
	)
	if err != nil {
		return err
	}

	println(h.ID().Pretty())

	// NATPortMap() EnableAutoRelay(), libp2p.EnableNATService(), DisableRelay(), ConnectionManager(...

	lite, err := ipfslite.New(ctx, datastore, h, dht, nil)
	if err != nil {
		return err
	}

	lite.Bootstrap(ipfslite.DefaultBootstrapPeers())

	// len(datastore.skynetMap)+len(datastore.values)
	keys := make([]string, 0)

	for k := range datastore.values {
		if strings.HasPrefix(k.String(), "/blocks/") {
			keys = append(keys, strings.Split(k.String(), "/blocks/")[1])
		}
	}

	for k := range datastore.skynetMap {
		keys = append(keys, strings.Split(k.String(), "/blocks/")[1])
	}

	const maxProvideWorkers = 512
	tokens := make(chan struct{}, maxProvideWorkers)
	for _, k := range keys {
		// fmt.Println("PROVIDE", k)

		dskey, err := base32.RawStdEncoding.DecodeString(k)
		if err != nil {
			return err
		}
		mhash, err := mh.Cast(dskey)
		if err != nil {
			return err
		}
		c := cid.NewCidV0(mhash)
		tokens <- struct{}{} // this will block when tokens is full
		go func() {
			defer func() {
				<-tokens // this will release one more space in tokens
			}()
			err := dht.Provide(context.Background(), c, true)
			if err != nil {
				fmt.Printf("Error providing %v: %v", c, err)
				return
			}
			//fmt.Println("Provided", c)
		}()
	}

	for i := maxProvideWorkers; i != 0; i-- {
		// block until all provides freed their spot in the channel
		tokens <- struct{}{}
	}
	fmt.Println("All provides finished")

	runtime.Goexit() // Quit main to avoid exiting the program (if main return the program exit, if we kill the goroutine instead it will never return)
	return nil
}

// AddParams contains all of the configurable parameters needed to specify the
// importing process of a file.
type AddParams struct {
	Layout    string
	Chunker   string
	RawLeaves bool
	Hidden    bool
	Shard     bool
	NoCopy    bool
	HashFun   string
}

// Here are some basic datastore implementations.

// SkynetDatastore uses a standard Go map for internal storage.
type SkynetDatastore struct {
	sync.RWMutex

	values    map[ds.Key][]byte
	skynetMap map[ds.Key]string

	client http.Client // use a shared client to do TCP connection reusing or HTTP2 muxing
}

var _ ds.Datastore = (*SkynetDatastore)(nil)
var _ ds.Batching = (*SkynetDatastore)(nil)

// NewSkynetDatastore constructs a SkynetDatastore. It is _not_ thread-safe by
// default, wrap using sync.MutexWrap if you need thread safety (the answer here
// is usually yes).
func NewSkynetDatastore() (d *SkynetDatastore) {
	fmt.Println("SkyDS()")
	return &SkynetDatastore{
		skynetMap: make(map[ds.Key]string),
		values:    make(map[ds.Key][]byte),
		client: http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Put implements Datastore.Put
func (d *SkynetDatastore) Put(ctx context.Context, key ds.Key, value []byte) (err error) {
	// fmt.Println("SkyDS.Put", key)

	d.Lock()
	defer d.Unlock()

	d.values[key] = value[:len(value):len(value)] // ensure capacity is unrecoverable to force allocations on append

	return nil
}

// Sync implements Datastore.Sync
func (d *SkynetDatastore) Sync(ctx context.Context, prefix ds.Key) error {
	// fmt.Println("SkyDS.Sync")
	return nil
}

// Get implements Datastore.Get
func (d *SkynetDatastore) Get(ctx context.Context, key ds.Key) (value []byte, err error) {
	d.RLock()
	val, found := d.values[key]
	if found {
		d.RUnlock()
		return val, nil
	}
	uri, found := d.skynetMap[key]
	d.RUnlock()

	if found {
		fmt.Println("SkyDS.Get", key, uri)

		u, err := url.Parse(uri)
		if err != nil {
			return nil, err
		}

		rang, err := getRangeStringFromSkynetUrl(u)
		if err != nil {
			return nil, err
		}
		size, err := computeSizesFromRange(rang)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequest("GET", "https://siasky.net/"+u.Host, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("range", "bytes="+rang)
		resp, err := d.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		// use buffer to avoid growth (preallocate the neede memory)
		var buf bytes.Buffer
		buf.Grow(size)
		_, err = buf.ReadFrom(resp.Body)
		if err != nil {
			return nil, err
		}

		if !(resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusPartialContent) {
			return nil, fmt.Errorf("non 200 response code: %d; body: %q; uri: %q", resp.StatusCode, buf.String(), uri)
		}

		b := buf.Bytes()
		if len(b) != size {
			return nil, fmt.Errorf("unexpected response size, got: %d; expected: %d", len(b), size)
		}

		return b, nil
	}
	return nil, ds.ErrNotFound

}

// Has implements Datastore.Has
func (d *SkynetDatastore) Has(ctx context.Context, key ds.Key) (exists bool, err error) {
	fmt.Println("SkyDS.Has", key)
	d.RLock()
	defer d.RUnlock()
	_, found := d.values[key]
	if found {
		return true, nil
	}

	_, found = d.skynetMap[key]
	if found {
		return true, nil
	}

	return false, nil
}

// GetSize implements Datastore.GetSize
func (d *SkynetDatastore) GetSize(ctx context.Context, key ds.Key) (size int, err error) {
	fmt.Println("SkyDS.GetSize")

	d.RLock()
	defer d.RUnlock()

	if v, found := d.skynetMap[key]; found {
		return getSizeFromSkynetValue(v)
	}
	if v, found := d.values[key]; found {

		return len(v), nil

	}

	return -1, ds.ErrNotFound
}

func getRangeStringFromSkynetUrl(u *url.URL) (string, error) {
	q := u.Query()
	rang, ok := q["range"]
	if !ok {
		return "", fmt.Errorf("range not found in url %v", u.String())
	}
	if len(rang) != 1 {
		return "", fmt.Errorf("too many ranges in url %q", u.String())
	}
	return rang[0], nil
}

func computeSizesFromRange(rang string) (int, error) {
	ranges := strings.Split(rang, "-")
	if len(ranges) != 2 {
		return 0, fmt.Errorf(`invalid range argument %q; expected 2 parts after "-" split`, rang[0])
	}

	start, err := strconv.Atoi(ranges[0])
	if err != nil {
		return 0, err
	}
	end, err := strconv.Atoi(ranges[1])
	if err != nil {
		return 0, err
	}

	return end - start, nil
}

func getSizeFromSkynetValue(v string) (size int, err error) {
	u, err := url.Parse(v)
	if err != nil {
		return 0, err
	}

	rang, err := getRangeStringFromSkynetUrl(u)
	if err != nil {
		return 0, err
	}
	return computeSizesFromRange(rang)
}

// Delete implements Datastore.Delete
func (d *SkynetDatastore) Delete(ctx context.Context, key ds.Key) (err error) {
	fmt.Println("SkyDS.Delete")
	// delete(d.values, key)
	// TODO Support Delete
	return nil
}

// Query implements Datastore.Query
func (d *SkynetDatastore) Query(ctx context.Context, q dsq.Query) (dsq.Results, error) {
	// fmt.Println("SkyDS.Query", q)

	d.RLock()
	defer d.RUnlock()

	re := make([]dsq.Entry, 0, len(d.skynetMap)+len(d.values))
	for k, v := range d.skynetMap {
		size, err := getSizeFromSkynetValue(v)
		if err != nil {
			return nil, err
		}
		e := dsq.Entry{Key: k.String(), Size: size}

		re = append(re, e)
	}

	for k, v := range d.values {
		e := dsq.Entry{Key: k.String(), Size: len(v)}
		if !q.KeysOnly {
			e.Value = v
		}
		re = append(re, e)
	}
	r := dsq.ResultsWithEntries(q, re)
	r = dsq.NaiveQueryApply(q, r)
	// fmt.Println("query results", r.)
	return r, nil
}

func (d *SkynetDatastore) Batch(ctx context.Context) (ds.Batch, error) {
	// fmt.Println("SkyDS.Batch")
	return ds.NewBasicBatch(d), nil
}

func (d *SkynetDatastore) Close() error {
	fmt.Println("SkyDS.Close")
	return nil
}
