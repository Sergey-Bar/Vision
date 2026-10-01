package detect

// A constant-velocity Kalman filter over a box's centre.
//
// # Why this exists, and what it costs
//
// A crossing box is a FUNCTION OF THE FRAMES AROUND IT once this exists. That is
// the whole point — a single frame's box jitters, and a jittery box flips sides of a
// line it is sitting on, so a car parked across a line can emit a crossing and then
// a second one a frame later as the box breathes.
//
// But it is also the thing the plan warns about: once the crossing box is
// filtered, it CANNOT be recomputed from the `decisions` table alone, which holds
// one row per incident and nothing about the track it came from. Replay therefore
// reads `box_observations` — the per-frame boxes — and re-runs the whole track.
// This file is why that table exists.
//
// # The model, and why this one
//
// Constant velocity in NORMALISED frame coordinates. Two independent 2-state
// filters, one per axis, each carrying (position, velocity) with a measurement of
// position only.
//
// Not a 4-state filter over both axes jointly, and not an 8-state filter over
// position and size. A detector's box size is noisy but it is not INDEPENDENT of
// its centre, and an 8-state filter would mean tuning six more numbers against a
// measurement whose size estimate is itself a fraction of a letterboxed pixel. The
// size is carried through UNFILTERED and the centre is filtered, so a reader looking
// at a crossing box can still see the width the model actually reported.
//
// The two axes are treated as independent, which is a simplification and is stated:
// a camera with rolling shutter produces cross-axis correlation, and modelling it
// needs a joint 4-state filter for a second-order effect on a POC whose subject is
// whether the detector finds crossings at all.
//
// # Why the initialisation matters more than the arithmetic
//
// A filter seeded with zero velocity starts by believing the object is stationary
// and takes several frames to acquire — which on a fast object means it LAGS for
// exactly the frames that matter. So the SECOND measurement establishes the
// velocity directly and the filter starts already moving. That is why `seeded` is a
// field and not a frame counter.

// Kalman is the filter state for one tracked object.
//
// It is a value and not a pointer so a Track can embed it, and so copying a Track
// — which the overlay does, once per frame per track — cannot leave two objects
// sharing one filter.
type Kalman struct {
	// cx and cy are the FILTERED centre, in normalised frame coordinates.
	cx, cy float64
	// vx and vy are velocity per SECOND, not per frame.
	//
	// Per second, and this is load-bearing: the engine is fed frames at whatever
	// rate the source produced, and a velocity in units of "per frame" silently
	// becomes a different prediction when the rate changes. A run replayed from
	// `box_observations` at a different rate would then produce a different crossing,
	// and the replay would be a lie dressed as a check.
	vx, vy float64
	// pxx/pyy are the position variances, pxv/pyv the position-velocity
	// covariances, and pvv the shared velocity variance. Both axes share pvv because
	// they share a process noise; a 1-D constant-velocity filter uses the same
	// velocity variance on both and splitting it would be tuning a parameter that
	// cannot be estimated from this measurement anyway.
	pxx, pyy float64
	pxv, pyv float64
	pvv      float64
	// seeded is whether the initial velocity has been established from two
	// measurements. Until it is, the prediction is a no-op and the state is the raw
	// measurement — a filter that has not acquired yet must not pretend to have a
	// prediction, or it lags its object for exactly the acquisition window.
	seeded bool
	// have is whether there is any state at all.
	have bool
}

// KalmanParams are the filter's tuning, and every one of them is a declared
// parameter rather than a constant.
type KalmanParams struct {
	// ProcessNoise is the assumed acceleration, as continuous white-noise intensity
	// in normalised units per second.
	//
	// This is the ONE number that says what the filter believes about the world. A
	// small value makes it slow to follow an object that turns; a large one makes it
	// follow noise. 0.5 is roughly "a vehicle may change its velocity by half a
	// frame-width per second squared", which is about right for a junction and wrong
	// for a motorway — and it is declared so somebody who knows the material can
	// change it in one place.
	ProcessNoise float64
	// MeasurementNoise is how much the detector's centre is believed to move, in
	// normalised units. This is the detector's own accuracy, and it is the number a
	// cross-decoder comparison would move.
	MeasurementNoise float64
	// InitialVelocityNoise is the assumed initial velocity variance. Large, because
	// the first two measurements ARE the estimate and treating them as precise would
	// make the filter over-confident about a velocity it has two samples of.
	InitialVelocityNoise float64
}

// DefaultKalman is the filter's tuning for a fixed camera on a road.
//
// The numbers are stated as claims about the material rather than left as "tuned":
// 0.5 process noise is about half a frame-width per second squared of permitted
// acceleration, and 0.02 measurement noise is two percent of the frame, which is
// about what a 640-pixel input gives you for a small object.
func DefaultKalman() KalmanParams {
	return KalmanParams{
		ProcessNoise:         0.5,
		MeasurementNoise:     0.02,
		InitialVelocityNoise: 1.0,
	}
}

// Init seeds the filter with its first measurement.
//
// There is no prediction on the first frame, so a single-frame track has exactly
// the raw measurement as its filtered centre — the honest answer, and what makes a
// one-frame track indistinguishable from an unfiltered one.
func (k *Kalman) Init(cx, cy float64, p KalmanParams) {
	*k = Kalman{
		cx: cx, cy: cy, have: true,
		pxx: p.MeasurementNoise, pyy: p.MeasurementNoise,
		pvv: p.InitialVelocityNoise,
		pxv: 0, pyv: 0,
	}
}

// Update folds one measurement in over an elapsed time and returns the filtered
// centre.
//
// `dt` is in SECONDS and the caller supplies it from the frame timestamps rather
// than from a nominal frame rate: a source that stalls for a second must widen the
// covariance rather than predict a second of motion nobody observed.
func (k *Kalman) Update(cx, cy, dt float64, p KalmanParams) (fx, fy float64) {
	if !k.have {
		k.Init(cx, cy, p)
		return k.cx, k.cy
	}
	if dt <= 0 {
		// A duplicate timestamp means two frames at the same instant, which is not a
		// real measurement interval. Using a nominal 1/30s rather than zero keeps
		// the prediction from dividing by zero, and the cost is one frame of
		// prediction error on a frame that carried no new information anyway.
		dt = 1.0 / 30.0
	}

	if !k.seeded {
		// The SECOND measurement establishes the velocity directly, over the REAL
		// elapsed time. This is what stops the filter lagging for its first several
		// frames, which on a vehicle at 10 m/s and 5 fps is a third of the object per
		// frame — large enough that the object reaches the line before the filter
		// believes it is moving, and the crossing is decided on a stale centre.
		k.vx = (cx - k.cx) / dt
		k.vy = (cy - k.cy) / dt
		k.cx, k.cy = cx, cy
		k.seeded = true
		k.spread(p)
		return k.cx, k.cy
	}

	// --- predict -------------------------------------------------------------
	// x' = F x with F = [[1, dt], [0, 1]], per axis.
	k.cx += k.vx * dt
	k.cy += k.vy * dt
	// P' = F P F' + Q, expanded by hand rather than through a matrix type.
	//
	// Q is the continuous white-noise-acceleration model:
	//
	//	Q = q * [[dt^4/4, dt^3/2], [dt^3/2, dt^2]]
	//
	// The dt^4 on position and dt^2 on velocity are what make the process noise
	// dimensionally a squared distance per second squared, and getting them wrong
	// produces a filter that either does not widen at all or diverges.
	q := p.ProcessNoise
	dt2 := dt * dt
	dt3 := dt2 * dt
	dt4 := dt2 * dt2
	k.pxx += 2*dt*k.pxv + dt2*k.pvv + q*dt4/4
	k.pyy += 2*dt*k.pyv + dt2*k.pvv + q*dt4/4
	k.pxv += dt*k.pvv + q*dt3/2
	k.pyv += dt*k.pvv + q*dt3/2
	k.pvv += q * dt2

	// --- correct -------------------------------------------------------------
	// H observes position only, so the innovation covariance is the position
	// variance plus the measurement variance, and the gain has two components: how
	// much of the residual goes to position, and how much of it goes to VELOCITY.
	//
	// The second component is the one that is easy to leave out and the one that
	// makes the difference: without it the velocity only ever comes from prediction
	// and never from evidence, so a turning object accumulates an unbounded lag.
	r := p.MeasurementNoise
	sx := k.pxx + r
	sy := k.pyy + r
	kx := k.pxx / sx
	ky := k.pyy / sy
	kvx := k.pxv / sx
	kvy := k.pyv / sy

	nx := cx - k.cx
	ny := cy - k.cy
	k.cx += kx * nx
	k.cy += ky * ny
	k.vx += kvx * nx
	k.vy += kvy * ny

	// P = (I - K) P. K is the column [kx, kvx], so this is a rank-one update.
	k.pxx *= 1 - kx
	k.pyy *= 1 - ky
	k.pxv *= 1 - kx
	k.pyv *= 1 - ky
	k.pvv -= kvx*k.pxv + kvy*k.pyv

	return k.cx, k.cy
}

// spread widens the covariance after a reseed, so the filter does not treat the two
// measurements it used to establish velocity as if they were a long history.
//
// Without this the filter would begin life over-confident about a velocity it has
// two samples of, and it would take longer than it should to correct after a turn.
func (k *Kalman) spread(p KalmanParams) {
	k.pxx = p.MeasurementNoise
	k.pyy = p.MeasurementNoise
	k.pxv = 0
	k.pyv = 0
	k.pvv = p.InitialVelocityNoise
}

// Velocity returns the filtered velocity per second, for the overlay's motion arrow
// and for a reader who wants to know whether a track is moving or parked.
func (k *Kalman) Velocity() (vx, vy float64) { return k.vx, k.vy }

// Centre returns the filtered centre, and reports whether there is one at all.
func (k *Kalman) Centre() (cx, cy float64, ok bool) { return k.cx, k.cy, k.have }

// FilteredBox is a track's measured box with its CENTRE replaced by the filter's
// estimate.
//
// Only the centre. See the file comment: the size is the model's, unfiltered.
func FilteredBox(measured Box, cx, cy float64) Box {
	hw := (measured.X2 - measured.X1) / 2
	hh := (measured.Y2 - measured.Y1) / 2
	return Box{
		Class: measured.Class, Score: measured.Score,
		X1: cx - hw, Y1: cy - hh, X2: cx + hw, Y2: cy + hh,
	}
}
