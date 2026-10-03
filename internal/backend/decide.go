package backend

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

type asked struct {
	res     *response
	verdict fields
	ms      int64
}

func millis(d time.Duration) int64 {
	return int64(math.Round(float64(d) / float64(time.Millisecond)))
}

func (c *Client) ask(ctx context.Context, e endpoint, body []byte, sp *space) (asked, error) {
	started := c.now()
	data, err := c.postWithRetry(ctx, e, body)
	if err != nil {
		return asked{}, err
	}
	ms := millis(c.now().Sub(started))
	res, err := parseResponse(data, c.redactor)
	if err != nil {
		return asked{}, err
	}
	verdict, err := interpret(res, sp)
	if err != nil {
		return asked{}, err
	}
	return asked{res: res, verdict: verdict, ms: ms}, nil
}

func (c *Client) escalationReason(v fields) string {
	switch v.operation {
	case opDone:
		return ReasonDone
	case opBlocked:
		return ReasonBlocked
	}
	if v.target != nil && v.targetProbabilities[*v.target] < c.targetConfidence {
		return ReasonTargetConf
	}
	// Strictly below, so op_conf 0.99 keeps a primary answer of exactly 0.99.
	if c.opConfidence > 0 && v.operationProbabilities[v.operation] < c.opConfidence {
		return ReasonOpConf
	}
	return ""
}

// describeFailure keeps the typed model errors as they are, so their text is what the trace records, and adds context
// to every other error.
func describeFailure(err error) error {
	var connection *ConnectionError
	var invalid *InvalidResponseError
	var status *HTTPStatusError
	if errors.As(err, &connection) || errors.As(err, &invalid) || errors.As(err, &status) {
		return err
	}
	return fmt.Errorf("decide: %w", err)
}

// Decide asks the model for the next operation and target, running the cascade when a verifier is configured.
func (c *Client) Decide(ctx context.Context, in Input) (Decision, error) {
	sp := buildSpace(in.State.Actions)
	request := buildRequest(c.model, in, sp)
	body, err := marshalRequest(request)
	if err != nil {
		return Decision{}, fmt.Errorf("encode model request: %w", err)
	}
	started := c.now()
	primary, err := c.ask(ctx, c.primary, body, sp)
	if err != nil {
		return Decision{}, describeFailure(err)
	}
	chosen, verdict := primary.res, primary.verdict
	latency := int64(-1)
	var cascade *Cascade
	if c.verifier != nil {
		if reason := c.escalationReason(primary.verdict); reason != "" {
			outcome, err := c.escalate(ctx, escalation{
				reason: reason, body: body, sp: sp, primary: primary, started: started,
				key: vetoKeyOf(request), cache: in.Cache, step: in.Step,
			})
			if err != nil {
				return Decision{}, describeFailure(err)
			}
			cascade = outcome.cascade
			if outcome.res != nil {
				chosen, verdict = outcome.res, outcome.verdict
			}
			latency = outcome.latency
		}
	}
	if latency < 0 {
		latency = millis(c.now().Sub(started))
	}
	return Decision{
		Choice:                 verdict.choice,
		Operation:              verdict.operation,
		Target:                 verdict.target,
		Confidence:             verdict.confidence,
		Probabilities:          verdict.probabilities,
		TargetIDs:              verdict.targetIDs,
		OperationProbabilities: verdict.operationProbabilities,
		TargetProbabilities:    verdict.targetProbabilities,
		TargetConfidence:       verdict.targetConfidence,
		RawAnswers:             chosen.Answers,
		Model:                  chosen.Model,
		Usage:                  chosen.usage(),
		ServerUsage:            chosen.Usage,
		LatencyMS:              latency,
		Request:                body,
		Cascade:                cascade,
		VetoKey:                vetoKeyOf(request),
	}, nil
}

type escalation struct {
	reason  string
	body    []byte
	sp      *space
	primary asked
	started time.Time
	key     VetoKey
	cache   *VetoCache
	step    int
}

type escalated struct {
	cascade *Cascade
	res     *response
	verdict fields
	latency int64
}

func (c *Client) escalate(ctx context.Context, esc escalation) (escalated, error) {
	primaryLeg := legOf(esc.primary.res, esc.primary.ms)
	cacheable := esc.cache != nil && (esc.reason == ReasonDone || esc.reason == ReasonBlocked)
	if cacheable {
		if entry, verdict, ok := esc.cache.lookup(esc.key, esc.sp); ok {
			return escalated{
				cascade: &Cascade{
					Reason:  esc.reason,
					Used:    UsedCache,
					Primary: primaryLeg,
					Cache: &CascadeHit{
						Answers: entry.res.Answers, Model: entry.res.Model, model: entry.res.modelRaw, FromStep: entry.step,
					},
				},
				res:     entry.res,
				verdict: verdict,
				latency: esc.primary.ms,
			}, nil
		}
	}
	verified, err := c.ask(ctx, endpoint(*c.verifier), esc.body, esc.sp)
	if err != nil {
		if ctx.Err() != nil {
			return escalated{}, err
		}
		elapsed := millis(c.now().Sub(esc.started)) - esc.primary.ms
		return escalated{
			cascade: &Cascade{
				Reason:   esc.reason,
				Used:     UsedPrimary,
				Primary:  primaryLeg,
				Verifier: &CascadeLeg{Error: err.Error(), LatencyMS: elapsed},
			},
			latency: -1,
		}, nil
	}
	if cacheable && verified.verdict.operation != esc.primary.verdict.operation {
		esc.cache.store(esc.key, verified.res, esc.step)
	}
	verifierLeg := legOf(verified.res, verified.ms)
	return escalated{
		cascade: &Cascade{
			Reason:   esc.reason,
			Used:     UsedVerifier,
			Primary:  primaryLeg,
			Verifier: &verifierLeg,
		},
		res:     verified.res,
		verdict: verified.verdict,
		latency: -1,
	}, nil
}
