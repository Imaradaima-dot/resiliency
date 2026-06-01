// scripts/mongo-init.js
// Called by the mongo-init container after all 4 nodes are healthy.
//
// Replica set topology:
//   us-east  → PRIMARY   priority=10  (preferred election winner)
//   us-west  → SECONDARY priority=5
//   europe   → SECONDARY priority=5
//   asia     → ARBITER   priority=0   (votes but holds no data)
//
// w:majority requires 2 of the 3 data-bearing nodes to ack every write.
// If us-east fails, us-west or europe is elected primary in < 15 seconds.

sleep(3000);

var cfg = {
  _id: "rs0",
  members: [
    { _id: 0, host: "mongo-us-east:27017", priority: 10 },
    { _id: 1, host: "mongo-us-west:27017", priority: 5  },
    { _id: 2, host: "mongo-europe:27017",  priority: 5  },
    { _id: 3, host: "mongo-asia:27017",    priority: 0, votes: 1 }
  ]
};

try {
  var status = rs.status();
  print("[mongo-init] Already initialised: " + status.set);
} catch (e) {
  var result = rs.initiate(cfg);
  print("[mongo-init] rs.initiate: " + JSON.stringify(result));

  var maxWait = 30;
  while (maxWait-- > 0) {
    sleep(1000);
    try { if (db.isMaster().ismaster) { print("[mongo-init] Primary elected."); break; } } catch (_) {}
  }
}

["resiliency_raw", "resiliency_processed", "resiliency_serving"].forEach(function(dbName) {
  var zoneDb = db.getSiblingDB(dbName);
  zoneDb.createCollection("_init");
  zoneDb._init.insertOne({ initialised: true, ts: new Date() });
  print("[mongo-init] Created zone: " + dbName);
});

print("[mongo-init] Done.");
