/* global React */
// ============================================================
// FRAMES — Real-looking mock UIs rendered inside each Figma frame
// on the canvas. Each takes (label, idx, frameTotal, leafId) and
// returns a hi-fi-ish mobile screen at 280×580 baseline.
// All measurements assume a phone-sized canvas; the parent
// CanvasFrame applies scale to fit.
// ============================================================
const F = window.React;

// ---------- shared atoms -----------
const StatusBar = () => (
  <div className="ph-status">
    <span className="ph-time">9:41</span>
    <span className="ph-icons">
      <svg width="14" height="9" viewBox="0 0 14 9"><path fill="currentColor" d="M0 8h2V6H0zm3 0h2V4H3zm3 0h2V2H6zm3 0h2V0H9z"/></svg>
      <svg width="14" height="9" viewBox="0 0 14 9"><path fill="none" stroke="currentColor" d="M1 4Q7-1 13 4M3 6Q7 2 11 6M5 8L7 7L9 8"/></svg>
      <svg width="22" height="10" viewBox="0 0 22 10"><rect x="0.5" y="0.5" width="18" height="9" rx="2" fill="none" stroke="currentColor"/><rect x="2" y="2" width="14" height="6" fill="currentColor"/><rect x="19" y="3" width="2" height="4" rx="0.5" fill="currentColor"/></svg>
    </span>
  </div>
);

const TopNav = ({ title, back = true, action = null }) => (
  <div className="ph-nav">
    {back && <span className="ph-back">‹</span>}
    <span className="ph-nav-title">{title}</span>
    {action || <span className="ph-action" />}
  </div>
);

const PrimaryCTA = ({ children, disabled = false }) => (
  <button className={`ph-cta ${disabled ? "is-disabled" : ""}`}>{children}</button>
);

const InputField = ({ label, value, hint, error = false }) => (
  <div className={`ph-input ${error ? "is-error" : ""}`}>
    <label className="ph-input-label">{label}</label>
    <div className="ph-input-field">{value}</div>
    {hint && <div className="ph-input-hint">{hint}</div>}
  </div>
);

const OTPRow = ({ filled = 0, total = 6 }) => (
  <div className="ph-otp">
    {Array.from({ length: total }).map((_, i) => (
      <div key={i} className={`ph-otp-cell ${i < filled ? "is-filled" : ""} ${i === filled ? "is-active" : ""}`}>
        {i < filled ? "•" : ""}
      </div>
    ))}
  </div>
);

const Card = ({ children, padded = true, className = "" }) => (
  <div className={`ph-card ${padded ? "is-padded" : ""} ${className}`}>{children}</div>
);

const Skeleton = ({ w = "100%", h = 14, mt = 8 }) => (
  <div className="ph-skel" style={{ width: w, height: h, marginTop: mt }} />
);

// ---------- frame variants ----------
function Frame_Pan({ idx, label }) {
  switch (idx) {
    case 0: return (
      <>
        <TopNav title="PAN verification" />
        <div className="ph-body">
          <div className="ph-eyebrow">STEP 1 OF 4</div>
          <h2 className="ph-h2">Enter your PAN</h2>
          <div className="ph-sub">We'll fetch your name and DOB from the income tax database.</div>
          <InputField label="PAN number" value="ABCDE1234F" />
          <div className="ph-helper">10-digit alphanumeric</div>
        </div>
        <div className="ph-foot"><PrimaryCTA>Verify PAN</PrimaryCTA></div>
      </>
    );
    case 1: return (
      <>
        <TopNav title="Verifying…" />
        <div className="ph-body ph-center">
          <div className="ph-spinner" />
          <h3 className="ph-h3" style={{ marginTop: 24 }}>Looking up your PAN</h3>
          <div className="ph-sub">This usually takes a few seconds.</div>
        </div>
      </>
    );
    case 2: return (
      <>
        <TopNav title="Confirm name" />
        <div className="ph-body">
          <div className="ph-eyebrow">STEP 2 OF 4</div>
          <h2 className="ph-h2">Is this you?</h2>
          <Card>
            <div className="ph-row"><span className="ph-row-k">Name</span><span className="ph-row-v">Aanya Patel</span></div>
            <div className="ph-row"><span className="ph-row-k">PAN</span><span className="ph-row-v">ABCDE1234F</span></div>
            <div className="ph-row"><span className="ph-row-k">Status</span><span className="ph-row-v ph-pos">✓ Verified</span></div>
          </Card>
        </div>
        <div className="ph-foot"><PrimaryCTA>Yes, that's me</PrimaryCTA></div>
      </>
    );
    case 3: return (
      <>
        <TopNav title="Date of birth" />
        <div className="ph-body">
          <div className="ph-eyebrow">STEP 3 OF 4</div>
          <h2 className="ph-h2">Confirm your DOB</h2>
          <div className="ph-dob">
            <div className="ph-dob-cell">14</div>
            <div className="ph-dob-cell">07</div>
            <div className="ph-dob-cell">1996</div>
          </div>
          <div className="ph-helper">Must match your PAN records</div>
        </div>
        <div className="ph-foot"><PrimaryCTA>Continue</PrimaryCTA></div>
      </>
    );
    case 4: return (
      <>
        <TopNav title="Review details" />
        <div className="ph-body">
          <div className="ph-eyebrow">STEP 4 OF 4</div>
          <h2 className="ph-h2">All set</h2>
          <div className="ph-sub">Verify everything's correct.</div>
          <Card>
            <div className="ph-row"><span className="ph-row-k">PAN</span><span className="ph-row-v">ABCDE1234F</span></div>
            <div className="ph-row"><span className="ph-row-k">Name</span><span className="ph-row-v">Aanya Patel</span></div>
            <div className="ph-row"><span className="ph-row-k">DOB</span><span className="ph-row-v">14 Jul 1996</span></div>
          </Card>
          <Card className="ph-card-soft">
            <div className="ph-disclaimer">By continuing, you authorise us to fetch your KYC from your demat broker and report it to the Investor Tax Department.</div>
          </Card>
        </div>
        <div className="ph-foot"><PrimaryCTA>Confirm</PrimaryCTA></div>
      </>
    );
    default: return (
      <>
        <TopNav title="PAN verified" />
        <div className="ph-body ph-center">
          <div className="ph-success">✓</div>
          <h3 className="ph-h3">Identity verified</h3>
          <div className="ph-sub">Your PAN matches our records.</div>
        </div>
        <div className="ph-foot"><PrimaryCTA>Continue to next step</PrimaryCTA></div>
      </>
    );
  }
}

function Frame_Otp({ idx }) {
  switch (idx) {
    case 0: return (
      <>
        <TopNav title="Aadhaar OTP" />
        <div className="ph-body">
          <h2 className="ph-h2">Verify your Aadhaar</h2>
          <div className="ph-sub">Enter your Aadhaar number. We'll send an OTP to your linked mobile.</div>
          <InputField label="Aadhaar number" value="1234 5678 9012" />
          <Card className="ph-card-soft">
            <div className="ph-disclaimer">Your Aadhaar number is masked & encrypted. We never store it.</div>
          </Card>
        </div>
        <div className="ph-foot"><PrimaryCTA>Send OTP</PrimaryCTA></div>
      </>
    );
    case 1: return (
      <>
        <TopNav title="Enter OTP" />
        <div className="ph-body">
          <h2 className="ph-h2">Enter the OTP</h2>
          <div className="ph-sub">Sent to +91 ••••••87 23</div>
          <OTPRow filled={3} />
          <div className="ph-helper">Resend in 0:42</div>
        </div>
        <div className="ph-foot"><PrimaryCTA disabled>Verify</PrimaryCTA></div>
      </>
    );
    case 2: return (
      <>
        <TopNav title="Verifying" />
        <div className="ph-body ph-center">
          <div className="ph-spinner" />
          <h3 className="ph-h3" style={{ marginTop: 20 }}>Confirming with UIDAI</h3>
        </div>
      </>
    );
    case 3: return (
      <>
        <TopNav title="Aadhaar verified" />
        <div className="ph-body ph-center">
          <div className="ph-success">✓</div>
          <h3 className="ph-h3">Aadhaar verified</h3>
          <div className="ph-sub">All your details have been fetched securely.</div>
        </div>
        <div className="ph-foot"><PrimaryCTA>Continue</PrimaryCTA></div>
      </>
    );
    default: return (
      <>
        <TopNav title="Resend OTP" />
        <div className="ph-body">
          <h3 className="ph-h3">Try again</h3>
          <div className="ph-sub">Choose how you'd like to receive the code.</div>
          <Card><div className="ph-list-row"><span>SMS</span><span className="ph-chev">›</span></div></Card>
          <Card><div className="ph-list-row"><span>Voice call</span><span className="ph-chev">›</span></div></Card>
          <Card><div className="ph-list-row"><span>WhatsApp</span><span className="ph-chev">›</span></div></Card>
        </div>
      </>
    );
  }
}

function Frame_Selfie({ idx }) {
  switch (idx) {
    case 0: return (
      <>
        <TopNav title="Liveness check" />
        <div className="ph-body">
          <h2 className="ph-h2">Quick selfie check</h2>
          <div className="ph-sub">A 3-second video to confirm it's you. No filters, please.</div>
          <div className="ph-illust">
            <div className="ph-face" />
            <div className="ph-face-ring" />
          </div>
          <div className="ph-tip">→ Look straight, good light, no glasses.</div>
        </div>
        <div className="ph-foot"><PrimaryCTA>I'm ready</PrimaryCTA></div>
      </>
    );
    case 1: return (
      <>
        <TopNav title="Camera" />
        <div className="ph-body ph-camera">
          <div className="ph-camera-frame">
            <div className="ph-face-live" />
            <div className="ph-record-pulse">REC · 0:02</div>
          </div>
          <div className="ph-tip">Slowly turn your head left and right.</div>
        </div>
      </>
    );
    case 2: return (
      <>
        <TopNav title="Capture preview" />
        <div className="ph-body">
          <div className="ph-illust ph-illust-photo">
            <div className="ph-face-photo" />
          </div>
          <div className="ph-sub">Looks good?</div>
        </div>
        <div className="ph-foot ph-foot-row">
          <button className="ph-cta-ghost">Retake</button>
          <PrimaryCTA>Use this</PrimaryCTA>
        </div>
      </>
    );
    default: return (
      <>
        <TopNav title="Verifying" />
        <div className="ph-body ph-center">
          <div className="ph-success">✓</div>
          <h3 className="ph-h3">Liveness confirmed</h3>
        </div>
      </>
    );
  }
}

function Frame_Bank({ idx }) {
  switch (idx) {
    case 0: return (
      <>
        <TopNav title="Add bank" />
        <div className="ph-body">
          <h2 className="ph-h2">Add your bank</h2>
          <div className="ph-sub">We'll deposit ₹1 to verify ownership.</div>
          <InputField label="Account number" value="XXXX XXXX 4521" />
          <InputField label="IFSC" value="HDFC0001234" />
        </div>
        <div className="ph-foot"><PrimaryCTA>Verify</PrimaryCTA></div>
      </>
    );
    case 1: return (
      <>
        <TopNav title="Verify IFSC" />
        <div className="ph-body">
          <h3 className="ph-h3">Bank found</h3>
          <Card>
            <div className="ph-bank-name">HDFC Bank</div>
            <div className="ph-row"><span className="ph-row-k">Branch</span><span className="ph-row-v">Andheri West</span></div>
            <div className="ph-row"><span className="ph-row-k">IFSC</span><span className="ph-row-v">HDFC0001234</span></div>
          </Card>
        </div>
        <div className="ph-foot"><PrimaryCTA>Confirm bank</PrimaryCTA></div>
      </>
    );
    case 2: return (
      <>
        <TopNav title="₹1 sent" />
        <div className="ph-body">
          <Card className="ph-card-info">
            <div className="ph-eyebrow">PENDING</div>
            <h3 className="ph-h3">We sent ₹1 to your bank</h3>
            <div className="ph-sub">Check your statement and enter the reference below.</div>
          </Card>
          <InputField label="Reference number" value="RZP9X4K2" hint="Last 8 chars of the txn ref" />
        </div>
        <div className="ph-foot"><PrimaryCTA>Confirm</PrimaryCTA></div>
      </>
    );
    default: return (
      <>
        <TopNav title="Bank verified" />
        <div className="ph-body ph-center">
          <div className="ph-success">✓</div>
          <h3 className="ph-h3">Bank linked</h3>
          <div className="ph-sub">HDFC ••••4521</div>
        </div>
        <div className="ph-foot"><PrimaryCTA>Continue</PrimaryCTA></div>
      </>
    );
  }
}

function Frame_Order({ idx }) {
  switch (idx) {
    case 0: return (
      <>
        <TopNav title="Buy · RELIANCE" action={<span className="ph-action">↗</span>} />
        <div className="ph-body">
          <Card className="ph-card-info">
            <div className="ph-row"><span className="ph-row-k">LTP</span><span className="ph-row-v ph-pos">₹2,847.30 ▲ 1.4%</span></div>
            <div className="ph-row"><span className="ph-row-k">Day range</span><span className="ph-row-v">2,820 — 2,861</span></div>
          </Card>
          <div className="ph-tabs">
            <span className="ph-tab is-active">Delivery</span>
            <span className="ph-tab">Intraday</span>
          </div>
          <InputField label="Quantity" value="10" />
          <InputField label="Price" value="2,847.30" hint="Limit · ₹0.05 ticks" />
        </div>
        <div className="ph-foot"><PrimaryCTA>Review order</PrimaryCTA></div>
      </>
    );
    case 1: return (
      <>
        <TopNav title="Confirm order" />
        <div className="ph-body">
          <Card>
            <h3 className="ph-h3">Buy 10 RELIANCE</h3>
            <div className="ph-row"><span className="ph-row-k">Price</span><span className="ph-row-v">₹2,847.30</span></div>
            <div className="ph-row"><span className="ph-row-k">Order value</span><span className="ph-row-v">₹28,473.00</span></div>
            <div className="ph-row"><span className="ph-row-k">Charges</span><span className="ph-row-v">₹4.20</span></div>
            <div className="ph-row ph-row-total"><span className="ph-row-k">Total</span><span className="ph-row-v">₹28,477.20</span></div>
          </Card>
        </div>
        <div className="ph-foot ph-foot-row">
          <button className="ph-cta-ghost">Edit</button>
          <PrimaryCTA>Place order</PrimaryCTA>
        </div>
      </>
    );
    case 2: return (
      <>
        <TopNav title="Placing…" />
        <div className="ph-body ph-center">
          <div className="ph-spinner" />
          <h3 className="ph-h3" style={{ marginTop: 20 }}>Sending to NSE</h3>
        </div>
      </>
    );
    case 3: return (
      <>
        <TopNav title="Order placed" />
        <div className="ph-body">
          <div className="ph-success ph-success-lg">✓</div>
          <h2 className="ph-h2 ph-center-text">Order placed</h2>
          <Card>
            <div className="ph-row"><span className="ph-row-k">Status</span><span className="ph-row-v ph-pos">Open</span></div>
            <div className="ph-row"><span className="ph-row-k">Order ID</span><span className="ph-row-v">#NS9X4K2</span></div>
          </Card>
        </div>
        <div className="ph-foot ph-foot-row">
          <button className="ph-cta-ghost">View order</button>
          <PrimaryCTA>Done</PrimaryCTA>
        </div>
      </>
    );
    default: return (
      <>
        <TopNav title="Order failed" />
        <div className="ph-body">
          <div className="ph-failure">!</div>
          <h2 className="ph-h2 ph-center-text">Couldn't place order</h2>
          <div className="ph-sub ph-center-text">Insufficient margin. Add ₹2,400 to continue.</div>
        </div>
        <div className="ph-foot"><PrimaryCTA>Add funds</PrimaryCTA></div>
      </>
    );
  }
}

function Frame_StockDetail({ idx, label }) {
  return (
    <>
      <TopNav title="RELIANCE" action={<span className="ph-action">⋯</span>} />
      <div className="ph-body ph-no-pad-top">
        <div className="ph-quote">
          <div className="ph-quote-price">₹2,847<span className="ph-quote-pct">.30</span></div>
          <div className="ph-quote-change">+₹39.20 (+1.4%) today</div>
        </div>
        <div className="ph-tabs ph-tabs-stretched">
          <span className={`ph-tab ${label.toLowerCase().includes("chart") ? "is-active" : ""}`}>Chart</span>
          <span className={`ph-tab ${label.toLowerCase().includes("about") ? "is-active" : ""}`}>About</span>
          <span className={`ph-tab ${label.toLowerCase().includes("financ") ? "is-active" : ""}`}>Financials</span>
          <span className={`ph-tab ${label.toLowerCase().includes("peer") ? "is-active" : ""}`}>Peers</span>
          <span className={`ph-tab ${label.toLowerCase().includes("news") ? "is-active" : ""}`}>News</span>
        </div>
        <div className="ph-chart">
          <svg width="100%" height="120" viewBox="0 0 240 120" preserveAspectRatio="none">
            <defs>
              <linearGradient id="ch" x1="0" x2="0" y1="0" y2="1">
                <stop offset="0%" stopColor="rgba(95,210,142,0.4)"/>
                <stop offset="100%" stopColor="rgba(95,210,142,0)"/>
              </linearGradient>
            </defs>
            <path d="M0 80 L20 70 L40 78 L60 60 L80 64 L100 50 L120 42 L140 48 L160 35 L180 40 L200 28 L220 30 L240 22 L240 120 L0 120 Z" fill="url(#ch)" />
            <path d="M0 80 L20 70 L40 78 L60 60 L80 64 L100 50 L120 42 L140 48 L160 35 L180 40 L200 28 L220 30 L240 22" fill="none" stroke="#5fd28e" strokeWidth="1.5"/>
          </svg>
          <div className="ph-tf"><span className="is-active">1D</span><span>1W</span><span>1M</span><span>1Y</span><span>5Y</span><span>All</span></div>
        </div>
        <Card>
          <div className="ph-row"><span className="ph-row-k">Market cap</span><span className="ph-row-v">₹19.3 LCr</span></div>
          <div className="ph-row"><span className="ph-row-k">P/E</span><span className="ph-row-v">28.4</span></div>
          <div className="ph-row"><span className="ph-row-k">52w high</span><span className="ph-row-v">₹3,024</span></div>
        </Card>
      </div>
      <div className="ph-foot ph-foot-row">
        <button className="ph-cta-ghost">Sell</button>
        <PrimaryCTA>Buy</PrimaryCTA>
      </div>
    </>
  );
}

function Frame_Fund({ idx, label }) {
  return (
    <>
      <TopNav title="Parag Parikh Flexi Cap" />
      <div className="ph-body ph-no-pad-top">
        <Card className="ph-card-info">
          <div className="ph-fund-name">Parag Parikh Flexi Cap</div>
          <div className="ph-fund-cat">Flexi Cap · Direct · Growth</div>
          <div className="ph-fund-nav">NAV ₹84.21 <span className="ph-pos">▲ 0.6%</span></div>
        </Card>
        <div className="ph-tabs ph-tabs-stretched">
          <span className={`ph-tab ${label.includes("Returns") ? "is-active" : ""}`}>Returns</span>
          <span className={`ph-tab ${label.includes("Risk") ? "is-active" : ""}`}>Risk</span>
          <span className={`ph-tab ${label.includes("Holdings") ? "is-active" : ""}`}>Holdings</span>
          <span className={`ph-tab ${label.includes("Manager") ? "is-active" : ""}`}>Manager</span>
        </div>
        <div className="ph-bars">
          <div className="ph-bar"><div className="ph-bar-fill" style={{ height: "30%" }} /><span>1Y</span></div>
          <div className="ph-bar"><div className="ph-bar-fill" style={{ height: "55%" }} /><span>3Y</span></div>
          <div className="ph-bar"><div className="ph-bar-fill" style={{ height: "78%" }} /><span>5Y</span></div>
          <div className="ph-bar"><div className="ph-bar-fill" style={{ height: "92%" }} /><span>10Y</span></div>
        </div>
        <Card>
          <div className="ph-row"><span className="ph-row-k">Min SIP</span><span className="ph-row-v">₹1,000</span></div>
          <div className="ph-row"><span className="ph-row-k">Min Lumpsum</span><span className="ph-row-v">₹1,000</span></div>
          <div className="ph-row"><span className="ph-row-k">Exit load</span><span className="ph-row-v">2% before 1Y</span></div>
        </Card>
      </div>
      <div className="ph-foot ph-foot-row">
        <button className="ph-cta-ghost">Lumpsum</button>
        <PrimaryCTA>Start SIP</PrimaryCTA>
      </div>
    </>
  );
}

function Frame_Sip({ idx }) {
  switch (idx) {
    case 0: case 1: return (
      <>
        <TopNav title="Start SIP" />
        <div className="ph-body">
          <h2 className="ph-h2">Monthly amount</h2>
          <div className="ph-amount">
            <span className="ph-amount-cur">₹</span>
            <span className="ph-amount-val">5,000</span>
          </div>
          <div className="ph-quick-amounts">
            <span>₹1,000</span><span className="is-active">₹5,000</span><span>₹10,000</span><span>₹25,000</span>
          </div>
          <Card><div className="ph-row"><span className="ph-row-k">SIP date</span><span className="ph-row-v">5th of every month ›</span></div></Card>
          <Card><div className="ph-row"><span className="ph-row-k">First debit</span><span className="ph-row-v">Today</span></div></Card>
        </div>
        <div className="ph-foot"><PrimaryCTA>Continue</PrimaryCTA></div>
      </>
    );
    case 2: return (
      <>
        <TopNav title="Set up mandate" />
        <div className="ph-body">
          <h2 className="ph-h2">Authorise auto-debit</h2>
          <div className="ph-sub">We'll debit ₹5,000 on the 5th of every month from your HDFC account.</div>
          <Card>
            <div className="ph-row"><span className="ph-row-k">Bank</span><span className="ph-row-v">HDFC ••••4521</span></div>
            <div className="ph-row"><span className="ph-row-k">Method</span><span className="ph-row-v">UPI AutoPay</span></div>
            <div className="ph-row"><span className="ph-row-k">Until</span><span className="ph-row-v">31 Dec 2099</span></div>
          </Card>
        </div>
        <div className="ph-foot"><PrimaryCTA>Open UPI app</PrimaryCTA></div>
      </>
    );
    default: return (
      <>
        <TopNav title="SIP active" />
        <div className="ph-body ph-center">
          <div className="ph-success ph-success-lg">✓</div>
          <h2 className="ph-h2 ph-center-text">SIP started</h2>
          <div className="ph-sub ph-center-text">Next debit on 5 Jun 2026</div>
        </div>
        <div className="ph-foot"><PrimaryCTA>Done</PrimaryCTA></div>
      </>
    );
  }
}

function Frame_Overview() {
  return (
    <>
      <TopNav title="Portfolio" action={<span className="ph-action">⋯</span>} />
      <div className="ph-body ph-no-pad-top">
        <Card className="ph-card-hero">
          <div className="ph-hero-eyebrow">CURRENT VALUE</div>
          <div className="ph-hero-num">₹12,84,302</div>
          <div className="ph-hero-change ph-pos">+₹1,42,109 · +12.4%</div>
        </Card>
        <div className="ph-row-grid">
          <div className="ph-mini-card"><div className="ph-mini-k">Invested</div><div className="ph-mini-v">₹11.4L</div></div>
          <div className="ph-mini-card"><div className="ph-mini-k">XIRR</div><div className="ph-mini-v ph-pos">14.2%</div></div>
          <div className="ph-mini-card"><div className="ph-mini-k">1Y</div><div className="ph-mini-v ph-pos">+18.1%</div></div>
        </div>
        <div className="ph-section-h">Top movers</div>
        <Card padded={false}>
          {[["RELIANCE","+1.4%"],["HDFC","-0.2%"],["TCS","+0.8%"]].map(([n, v], i) => (
            <div key={i} className="ph-list-row ph-list-row-padded">
              <span>{n}</span><span className={v.startsWith("-") ? "ph-neg" : "ph-pos"}>{v}</span>
            </div>
          ))}
        </Card>
      </div>
    </>
  );
}

function Frame_Login({ idx }) {
  if (idx === 0) return (
    <>
      <div className="ph-body ph-pad-top-lg">
        <div className="ph-logo">●</div>
        <h2 className="ph-h2 ph-center-text">Welcome back</h2>
        <div className="ph-sub ph-center-text">Sign in with your phone number.</div>
        <InputField label="Phone number" value="+91 98765 43210" />
      </div>
      <div className="ph-foot"><PrimaryCTA>Send OTP</PrimaryCTA></div>
    </>
  );
  if (idx === 1) return (
    <>
      <TopNav title="Enter OTP" />
      <div className="ph-body">
        <h2 className="ph-h2">Enter the OTP</h2>
        <div className="ph-sub">Sent to +91 ••••••43 10</div>
        <OTPRow filled={4} />
      </div>
      <div className="ph-foot"><PrimaryCTA>Verify</PrimaryCTA></div>
    </>
  );
  return (
    <>
      <TopNav title="MPIN" />
      <div className="ph-body">
        <h2 className="ph-h2">Enter your MPIN</h2>
        <OTPRow filled={2} total={4} />
        <a className="ph-link">Forgot MPIN?</a>
      </div>
    </>
  );
}

function Frame_List({ idx, label }) {
  return (
    <>
      <TopNav title={label} />
      <div className="ph-body">
        <Card padded={false}>
          {[1,2,3,4,5,6].map(i => (
            <div key={i} className="ph-list-row ph-list-row-padded">
              <div className="ph-avatar" style={{ background: `hsl(${i * 47}, 30%, 70%)` }}>{String.fromCharCode(64 + i)}</div>
              <div className="ph-list-text">
                <div className="ph-list-title">Item {i}</div>
                <div className="ph-list-sub">Subtitle text</div>
              </div>
              <span className="ph-chev">›</span>
            </div>
          ))}
        </Card>
      </div>
    </>
  );
}

function Frame_Generic({ idx, label, frameKind }) {
  // Generic skeleton-ish but shaped to label
  return (
    <>
      <TopNav title={label} />
      <div className="ph-body">
        <h2 className="ph-h2">{label}</h2>
        <div className="ph-sub">Mock content for the "{frameKind}" pattern.</div>
        <Card>
          <Skeleton w="60%" h={16} />
          <Skeleton w="80%" h={12} />
          <Skeleton w="40%" h={12} />
        </Card>
        <Card>
          <Skeleton w="50%" h={14} />
          <Skeleton w="90%" h={10} />
          <Skeleton w="70%" h={10} />
          <Skeleton w="55%" h={10} />
        </Card>
      </div>
      <div className="ph-foot"><PrimaryCTA>Continue</PrimaryCTA></div>
    </>
  );
}

// ---------- dispatcher ----------
window.PhoneFrame = function PhoneFrame({ kind, idx, label, total }) {
  let body;
  switch (kind) {
    case "pan": body = <Frame_Pan idx={idx} label={label} />; break;
    case "otp": body = <Frame_Otp idx={idx} />; break;
    case "selfie": body = <Frame_Selfie idx={idx} />; break;
    case "bank": body = <Frame_Bank idx={idx} />; break;
    case "order": body = <Frame_Order idx={idx} />; break;
    case "stockd": body = <Frame_StockDetail idx={idx} label={label} />; break;
    case "fund": body = <Frame_Fund idx={idx} label={label} />; break;
    case "sip": body = <Frame_Sip idx={idx} />; break;
    case "overview": body = <Frame_Overview />; break;
    case "login": body = <Frame_Login idx={idx} />; break;
    case "list": body = <Frame_List idx={idx} label={label} />; break;
    default: body = <Frame_Generic idx={idx} label={label} frameKind={kind} />;
  }
  return (
    <div className="ph-screen">
      <StatusBar />
      {body}
      <div className="ph-home-bar" />
    </div>
  );
};
