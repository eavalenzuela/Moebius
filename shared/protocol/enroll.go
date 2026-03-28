package protocol

// EnrollRequest is sent by the agent during first registration (POST /v1/agents/enroll).
type EnrollRequest struct {
	EnrollmentToken string `json:"enrollment_token"`
	CSR             string `json:"csr"` // PEM-encoded
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	OSVersion       string `json:"os_version"`
	Arch            string `json:"arch"`
	AgentVersion    string `json:"agent_version"`
}

// EnrollResponse is returned by the server after successful enrollment.
type EnrollResponse struct {
	AgentID             string `json:"agent_id"`
	Certificate         string `json:"certificate"` // PEM-encoded signed cert
	CAChain             string `json:"ca_chain"`    // PEM-encoded intermediate + root
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
}

// RenewRequest is sent by the agent to renew its certificate (POST /v1/agents/renew).
type RenewRequest struct {
	CSR string `json:"csr"` // PEM-encoded
}

// RenewResponse is returned by the server after successful renewal.
type RenewResponse struct {
	Certificate string `json:"certificate"` // PEM-encoded signed cert
	CAChain     string `json:"ca_chain"`    // PEM-encoded intermediate + root
}
