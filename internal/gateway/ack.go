package gateway

// answeredTypesSQL lists the message types that answer a request: a reply, or
// an ack that closes it with no delivery. Every query that asks whether a
// request is still open, or that counts an answer, uses it so the two kinds
// cannot drift apart.
const answeredTypesSQL = `('reply','ack')`
