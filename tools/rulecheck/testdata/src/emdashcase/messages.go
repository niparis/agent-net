package emdashcase

// A message a user reads. The escape makes the value hold an em dash.
const waiting = "Waiting \u2014 the gateway is busy" // want `em dash in a string literal`

const clean = "Waiting - the gateway is busy"
