import { useState } from 'react'
import { Camera, Plus, Radar } from 'lucide-react'
import { Page, PageHeader } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Field'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/States'
import { KeyValue } from '@/components/ui/Table'
import { useDevices, useProbeDevice, useRegisterDevice } from '@/hooks/product'
import { describeError } from '@/lib/errors'

// The device registry.
//
// This is the FIRST screen of "plug and play": the operator adds a camera,
// the product MEASURES it, and the screen says what was established.
//
// The discipline the page keeps is the discipline the product keeps. A camera
// NAMES the profiles it supports and is routinely wrong about it, so:
//
//   - a device that has never been measured says "not probed", and NOT
//     "no capabilities" — a camera that can do nothing and a camera nobody
//     asked are different facts;
//   - only a VERIFIED capability is shown as a capability, and a contradicted
//     one is shown as a finding about the camera;
//   - the address the product probes at is the REGISTERED address, and a
//     camera whose address the product does not know cannot be probed at all.
export function DevicesPage() {
  const { data, isPending, isError, error } = useDevices()
  const [adding, setAdding] = useState(false)
  const probe = useProbeDevice()
  const register = useRegisterDevice()

  if (isPending) {
    return (
      <Page>
        <PageHeader title="Devices" subtitle="What the product is responsible for" />
        <LoadingState label="Reading the device registry" />
      </Page>
    )
  }

  if (isError) {
    return (
      <Page>
        <PageHeader title="Devices" subtitle="What the product is responsible for" />
        <ErrorState
          title="The device registry did not answer"
          detail={
            <>
              <p>
                The console asked the product what devices it is responsible for and could not
                read the answer. The list below is empty because the product did not say, not
                because there are no devices.
              </p>
              <p className="mt-2 font-mono text-[11px] text-text-dim">{describeError(error)}</p>
            </>
          }
        />
      </Page>
    )
  }

  const devices = data?.items ?? []

  return (
    <Page>
      <PageHeader
        title="Devices"
        subtitle="What the product is responsible for"
        lede="A camera is a device, and a device is three things: what it is, what was measured about it, and where its credential lives. Only the measured part is a capability."
        actions={
          <Button variant="primary" size="sm" onClick={() => setAdding((v) => !v)}>
            <Plus size={14} aria-hidden="true" />
            Add a device
          </Button>
        }
      />

      {adding && (
        <Section>
          <AddDevice
            onDone={() => setAdding(false)}
            onSubmit={(input) => register.mutateAsync(input)}
            error={register.error}
            pending={register.isPending}
          />
        </Section>
      )}

      <Section>
        <Panel
          title="The registry"
          action={
            <span className="font-mono text-[11px] text-text-dim">
              {devices.length} device{devices.length === 1 ? '' : 's'}
            </span>
          }
        >
          {devices.length === 0 ? (
            <EmptyState
              title="No device is registered"
              detail="The product is responsible for nothing yet. That is a fact about the site, and it is not the same as a site whose registry is not configured — the product says which one this is."
            />
          ) : (
            <ul className="divide-y divide-line">
              {devices.map((d) => (
                <li key={d.deviceId} className="py-4 first:pt-0 last:pb-0">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <Camera size={15} className="text-text-dim" aria-hidden="true" />
                        <span className="font-mono text-[13px] text-text">{d.deviceId}</span>
                        {d.probed ? (
                          <Badge tone="ok">
                            <span aria-hidden="true">✓</span> measured
                          </Badge>
                        ) : (
                          <Badge tone="unknown">
                            <span aria-hidden="true">?</span> not probed
                          </Badge>
                        )}
                        {!d.enabled && (
                          <Badge tone="warn">
                            <span aria-hidden="true">!</span> disabled
                          </Badge>
                        )}
                      </div>
                      <p className="mt-1 text-[13px] leading-[20px] text-text-muted">
                        {[d.make, d.model].filter(Boolean).join(' ') || d.kind}
                        {d.firmware ? ` · firmware ${d.firmware}` : ''}
                      </p>
                    </div>
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={probe.isPending}
                      onClick={() => probe.mutate(d.deviceId)}
                      title="Measure this device and record what it established"
                    >
                      <Radar size={14} aria-hidden="true" />
                      Probe
                    </Button>
                  </div>

                  {d.probed ? (
                    <div className="mt-3">
                      {d.capabilities.length > 0 ? (
                        <ul className="flex flex-wrap gap-2">
                          {d.capabilities.map((c) => (
                            <li key={c}>
                              <Badge tone="info">
                                <span aria-hidden="true">◆</span> {c}
                              </Badge>
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <p className="text-[13px] leading-[20px] text-text-dim">
                          Measured, and nothing was established. The probe ran and the device
                          answered with no capability it could back up.
                        </p>
                      )}
                      {d.probedAt && (
                        <p className="mt-2 font-mono text-[11px] text-text-dim">
                          probed {d.probedAt}
                          {d.probeId ? ` · ${d.probeId}` : ''}
                        </p>
                      )}
                    </div>
                  ) : (
                    <p className="mt-3 text-[13px] leading-[20px] text-text-dim">
                      Nobody has measured this device. Until somebody does, it has no
                      capabilities — which is not the same as having none.
                    </p>
                  )}
                </li>
              ))}
            </ul>
          )}
          {probe.isError && (
            <p className="mt-4 text-[13px] leading-[20px] text-bad-ink">
              The probe did not complete: {describeError(probe.error)}
            </p>
          )}
        </Panel>
      </Section>

      {data?.vocabulary && (
        <Section>
          <Panel title="What a capability can be">
            <KeyValue
              items={[
                {
                  key: 'capability states',
                  value: data.vocabulary.capabilityStates.join(', '),
                },
                {
                  key: 'ONVIF capabilities',
                  value: data.vocabulary.onvifCapabilities.join(', '),
                },
                {
                  key: 'ONVIF profiles',
                  value: data.vocabulary.onvifProfiles.join(', '),
                },
                {
                  key: 'credential purposes',
                  value: data.vocabulary.credentialPurposes.join(', '),
                },
              ]}
            />
            <p className="mt-4 text-[13px] leading-[20px] text-text-dim">
              These are the CLOSED sets. A camera names the profiles it supports and is
              routinely wrong about it, so only the state <span className="font-mono">verified</span>{' '}
              counts as a capability — <span className="font-mono">contradicted</span> is a
              finding about the camera, and <span className="font-mono">unverified</span> is a
              claim nobody has checked.
            </p>
          </Panel>
        </Section>
      )}
    </Page>
  )
}

// AddDevice registers a camera.
//
// The address is normalised by the product, and a malformed one is refused
// HERE rather than three steps later by a probe against a host that does not
// exist — a typo caught when the operator typed it is a typo they can fix.
function AddDevice({
  onDone,
  onSubmit,
  error,
  pending,
}: {
  onDone: () => void
  onSubmit: (input: { deviceId: string; address?: string; kind?: string }) => Promise<unknown>
  error: unknown
  pending: boolean
}) {
  const [deviceId, setDeviceId] = useState('')
  const [address, setAddress] = useState('')

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!deviceId.trim()) return
    void onSubmit({ deviceId: deviceId.trim(), address: address.trim() || undefined }).then(onDone)
  }

  return (
    <Panel title="Add a device">
      <form onSubmit={submit} className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="block" htmlFor="device-id">
            <span className="mb-1 block text-[13px] font-medium text-text">Device id</span>
            <Input
              id="device-id"
              value={deviceId}
              onChange={(e) => setDeviceId(e.target.value)}
              placeholder="cam-entrance"
              aria-label="Device id"
            />
          </label>
          <label className="block" htmlFor="device-address">
            <span className="mb-1 block text-[13px] font-medium text-text">Address</span>
            <Input
              id="device-address"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              placeholder="192.0.2.10"
              aria-label="Address"
            />
          </label>
        </div>
        <p className="text-[13px] leading-[20px] text-text-dim">
          The address is a host and a port. It carries no credential: the camera's secret lives
          in the keystore and is named by a reference, never here and never in a log.
        </p>
        {error ? (
          <p className="text-[13px] leading-[20px] text-bad-ink">
            The device was not registered: {describeError(error)}
          </p>
        ) : null}
        <div className="flex items-center gap-3">
          <Button type="submit" variant="primary" size="sm" disabled={pending || !deviceId.trim()}>
            Register
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={onDone}>
            Cancel
          </Button>
        </div>
      </form>
    </Panel>
  )
}
