package dbmigrate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	// mongoScheme and mongoSRVScheme are the two MongoDB URL schemes.
	mongoScheme    = "mongodb"
	mongoSRVScheme = "mongodb+srv"
	// mongoDefaultPort is used when a host names no port.
	mongoDefaultPort = "27017"
	// mongoApplicationName tags the connection in the server's log.
	mongoApplicationName = "touchgrass"
	// mongoDialTimeout bounds finding a server and connecting to it.
	// The driver's own default is 30 seconds, too long for a status read.
	mongoDialTimeout = 5 * time.Second
	// migrateMongoChangelog is the collection migrate-mongo records
	// applied migrations in unless its config names another.
	migrateMongoChangelog = "changelog"
	// migrateMongoChangelogParam is the URL parameter, private to this
	// tool, that names a non-default changelog collection.
	migrateMongoChangelogParam = "changelogCollection"
	// migrateMongoHistoryLimit bounds one read of the changelog.
	migrateMongoHistoryLimit = 5000
	// mongoNameMax bounds a database or collection name.
	mongoNameMax = 120
)

// mongoParams are the URL options passed on to the driver, matched
// without regard to case as MongoDB does. Every other option is
// dropped: none of the rest is needed to read one collection, and
// write-side options (w, retryWrites, journal) have no business here.
var mongoParams = []string{
	"authSource", "authMechanism", "replicaSet", "directConnection",
	"readPreference", "tls", "ssl", "tlsCAFile", "tlsCertificateKeyFile",
}

// mongoTarget is a MongoDB URL reduced to what the reader needs.
type mongoTarget struct {
	// dsn is the URL with only driver options left.
	dsn        string
	hosts      []string
	database   string
	collection string
}

// label is the credential-free description of the target.
func (t mongoTarget) label() string {
	return fmt.Sprintf("%s/%s (collection %s)", strings.Join(t.hosts, ","), t.database, t.collection)
}

// parseMongoURL validates a MongoDB URL. It is read by hand because a
// MongoDB URL may list several hosts, which net/url refuses. The error
// never echoes the URL, which holds a password.
func parseMongoURL(databaseURL string) (mongoTarget, error) {
	scheme, rest, found := strings.Cut(strings.TrimSpace(databaseURL), "://")
	if !found {
		return mongoTarget{}, fmt.Errorf("%w: it has no scheme", ErrInvalidURL)
	}

	if scheme != mongoScheme && scheme != mongoSRVScheme {
		return mongoTarget{}, fmt.Errorf("%w: scheme %q", ErrUnsupportedDatabase, scheme)
	}

	rest, _, _ = strings.Cut(rest, "#")
	rest, rawQuery, _ := strings.Cut(rest, "?")
	authority, rawDatabase, _ := strings.Cut(rest, "/")

	userinfo, hostList := "", authority
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		userinfo, hostList = authority[:at+1], authority[at+1:]
	}

	hosts, err := mongoHosts(hostList, scheme == mongoSRVScheme)
	if err != nil {
		return mongoTarget{}, err
	}

	database, err := url.PathUnescape(rawDatabase)
	if err != nil || !validMongoName(database) || strings.Contains(database, ".") {
		return mongoTarget{}, fmt.Errorf("%w: it needs a database name after the host", ErrInvalidURL)
	}

	kept, collection, err := mongoOptions(rawQuery)
	if err != nil {
		return mongoTarget{}, err
	}

	dsn := scheme + "://" + userinfo + hostList + "/" + url.PathEscape(database)
	if len(kept) > 0 {
		dsn += "?" + kept.Encode()
	}

	return mongoTarget{dsn: dsn, hosts: hosts, database: database, collection: collection}, nil
}

// mongoOptions splits a URL's options into the ones passed on to the
// driver and the changelog collection the tool's own parameter names.
func mongoOptions(rawQuery string) (kept url.Values, collection string, err error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, "", fmt.Errorf("%w: its options do not parse", ErrInvalidURL)
	}

	collection = migrateMongoChangelog
	kept = url.Values{}

	for key, values := range query {
		switch {
		case key == migrateMongoChangelogParam:
			collection = values[0]
		case mongoParamAllowed(key) && values[0] != "":
			kept.Set(key, values[0])
		}
	}

	if !validMongoName(collection) || strings.HasPrefix(collection, "system.") {
		return nil, "", fmt.Errorf("%w: %s is not a collection name", ErrInvalidURL, migrateMongoChangelogParam)
	}

	return kept, collection, nil
}

// mongoHosts validates the host list and returns it with ports filled
// in. A DNS seed-list URL names exactly one host and no port.
func mongoHosts(hostList string, srv bool) ([]string, error) {
	hosts := strings.Split(hostList, ",")

	if srv {
		if len(hosts) != 1 || hosts[0] == "" || strings.Contains(hosts[0], ":") {
			return nil, fmt.Errorf("%w: a %s url names one host and no port", ErrInvalidURL, mongoSRVScheme)
		}

		return hosts, nil
	}

	for index, host := range hosts {
		name, port := host, mongoDefaultPort

		// A colon outside brackets is a port; inside, an IPv6 address.
		if strings.LastIndex(host, ":") > strings.LastIndex(host, "]") {
			var err error

			name, port, err = net.SplitHostPort(host)
			if err != nil {
				return nil, fmt.Errorf("%w: a host does not parse", ErrInvalidURL)
			}
		}

		name = strings.Trim(name, "[]")
		if name == "" || port == "" {
			return nil, fmt.Errorf("%w: it needs a host and a database name", ErrInvalidURL)
		}

		hosts[index] = net.JoinHostPort(name, port)
	}

	return hosts, nil
}

// mongoParamAllowed reports whether a URL option is passed on.
func mongoParamAllowed(key string) bool {
	for _, param := range mongoParams {
		if strings.EqualFold(param, key) {
			return true
		}
	}

	return false
}

// validMongoName reports whether name can be a database or collection
// name: not empty, bounded, and free of the characters MongoDB forbids.
func validMongoName(name string) bool {
	return name != "" && len(name) <= mongoNameMax && !strings.ContainsAny(name, "/\\ \"$\x00")
}

// changelogRow is one document of the migrate-mongo changelog.
type changelogRow struct {
	FileName  string    `bson:"fileName"`
	FileHash  string    `bson:"fileHash"`
	AppliedAt time.Time `bson:"appliedAt"`
}

// readMigrateMongo reads the changelog with one find over a client it
// disconnects before returning. It issues no other command, so a user
// with only the read role is enough. A database migrate-mongo has never
// run against has no collection, which reads as an empty history.
func readMigrateMongo(ctx context.Context, target mongoTarget) (records []Record, err error) {
	clientOptions := options.Client().
		ApplyURI(target.dsn).
		SetAppName(mongoApplicationName).
		SetServerSelectionTimeout(mongoDialTimeout).
		SetConnectTimeout(mongoDialTimeout).
		SetMaxPoolSize(1)

	client, err := mongo.Connect(clientOptions)
	if err != nil {
		// The driver's message can quote the URL; keep it out.
		return nil, fmt.Errorf("%w: the driver rejected it", ErrInvalidURL)
	}

	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()

		err = errors.Join(err, client.Disconnect(closeCtx))
	}()

	cursor, err := client.Database(target.database).Collection(target.collection).Find(ctx, bson.D{},
		options.Find().
			SetSort(bson.D{{Key: "appliedAt", Value: 1}, {Key: "fileName", Value: 1}}).
			SetProjection(bson.D{{Key: "fileName", Value: 1}, {Key: "fileHash", Value: 1}, {Key: "appliedAt", Value: 1}}).
			SetLimit(migrateMongoHistoryLimit))
	if err != nil {
		return nil, fmt.Errorf("reading migrate-mongo changelog: %w", err)
	}

	var rows []changelogRow
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("decoding migrate-mongo changelog: %w", err)
	}

	return changelogRecords(rows), nil
}

// changelogRecords maps changelog documents onto history rows. The
// changelog holds one applied-at time, so start and finish are equal.
func changelogRecords(rows []changelogRow) []Record {
	records := make([]Record, 0, len(rows))

	for _, row := range rows {
		if row.FileName == "" {
			continue
		}

		applied := row.AppliedAt.UTC()

		records = append(records, Record{
			Name:       row.FileName,
			Checksum:   row.FileHash,
			StartedAt:  applied,
			FinishedAt: &applied,
		})
	}

	return records
}
