package main

// This example launches an IPFS-Lite peer and fetches a hello-world
// hash from the IPFS network.

import (
	// "bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multiformats/go-base32"
	mh "github.com/multiformats/go-multihash"

	// "io/ioutil"

	ipfslite "github.com/hsanjuan/ipfs-lite"
	"github.com/ipfs/go-cid"

	// "github.com/ipfs/go-cid"

	// "github.com/ipfs/go-cid"

	crypto "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/multiformats/go-multiaddr"

	// "log"

	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
)

func main() {
	dbPath := "data/db.json"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// dssync.MutexWrap

	datastore := NewSkynetDatastore()
	buf, err := ioutil.ReadFile(dbPath)

	if err != nil {
		panic(err)
	}

	var m map[string]string

	// Unmarshal or Decode the JSON to the interface.
	json.Unmarshal(buf, &m)

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

	priv, _, err := crypto.GenerateEd25519Key(r)

	// crypto.KeyPairFromStdKey()
	if err != nil {
		panic(err)
	}

	listen, _ := multiaddr.NewMultiaddr("/ip4/0.0.0.0/tcp/4005")

	h, dht, err := ipfslite.SetupLibp2p(
		ctx,
		priv,
		nil,
		[]multiaddr.Multiaddr{listen},
		datastore,
		ipfslite.Libp2pOptionsExtra...,
	)

	println(h.ID().Pretty())
	println(h.ID().Loggable())
	println(h.ID().String())

	// NATPortMap() EnableAutoRelay(), libp2p.EnableNATService(), DisableRelay(), ConnectionManager(...

	// dht.Ena

	if err != nil {
		panic(err)
	}

	lite, err := ipfslite.New(ctx, datastore, h, dht, nil)
	if err != nil {
		panic(err)
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

	for _, k := range keys {
		// fmt.Println("PROVIDE", k)

		dskey, _ := base32.RawStdEncoding.DecodeString(k)
		mhash, _ := mh.Cast(dskey)
		c := cid.NewCidV0(mhash)
		fmt.Println("PROVIDE", c)
		dht.Provide(context.Background(), c, true)

	}

	for {
		fmt.Println("Infinite Loop 1")
		time.Sleep(time.Second * 30)
	}
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
	}
}

// Put implements Datastore.Put
func (d *SkynetDatastore) Put(ctx context.Context, key ds.Key, value []byte) (err error) {
	// fmt.Println("SkyDS.Put", key)

	d.Lock()
	defer d.Unlock()

	d.values[key] = value

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
	d.RUnlock()
	if found {
		return val, nil
	}

	uri, found := d.skynetMap[key]

	if found {
		fmt.Println("SkyDS.Get", key, uri)

		parts := strings.Split(uri[6:], "?range=")

		fmt.Println(parts)

		client := &http.Client{}
		req, _ := http.NewRequest("GET", "https://siasky.net/"+parts[0], nil)
		req.Header.Set("range", "bytes="+parts[1])
		resp, _ := client.Do(req)

		if err != nil {
			panic(err)
		}

		defer resp.Body.Close()

		b, err := io.ReadAll(resp.Body)
		// b, err := ioutil.ReadAll(resp.Body)  Go.1.15 and earlier
		if err != nil {
			log.Fatalln(err)
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
		u, err := url.Parse(v)
		if err != nil {
			return 0, err
		}

		q := u.Query()
		rang, ok := q["range"]
		if !ok {
			return 0, fmt.Errorf("range not found in url %q", v)
		}
		if len(rang) != 1 {
			return 0, fmt.Errorf("too many ranges in url %q", v)
		}
		ranges := strings.Split(rang[0], "-")
		if len(ranges) != 2 {
			return 0, fmt.Errorf("invalid range argument %q", rang[0])
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
	if v, found := d.values[key]; found {

		return len(v), nil

	}

	return -1, ds.ErrNotFound
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
	for k, _ := range d.skynetMap {
		e := dsq.Entry{Key: k.String(), Size: 262144}

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
