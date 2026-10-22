import { useParams, Link } from 'react-router-dom'
import { Page, PageHeader } from '@/components/Shell'
import { Panel, Section } from '@/components/ui/Panel'
import { EmptyState } from '@/components/ui/States'
import { KeyValue } from '@/components/ui/Table'
import { Badge } from '@/components/ui/Badge'
import { useEvidenceMetadata } from '@/hooks/product'

// The evidence.
//
// One moment, as the product holds it.
//
// The BYTES are the product's: the console links to the product's own content
// route and shows the frame it serves. A second reader of an evidence file is
// a second opinion about an integrity check, and the console does not hold
// opinions about digests it has not computed.
//
// The METADATA is the product's too, and this page used to render a dash for
// every field of it with a note explaining why. The note was honest and the
// page was empty, and the digest in particular is the one field an operator
// has to be able to read BEFORE they trust a clip. So the product serves the
// row, and the console reads it.
export function EvidencePage() {
  const { id = '' } = useParams()
  const contentUrl = `/v1/evidence/${encodeURIComponent(id)}/content`
  const { data, isPending, isError } = useEvidenceMetadata(id)

  return (
    <Page>
      <PageHeader
        eyebrow="Evidence"
        title={id}
        lede="The moment itself, served by the product. The console links to it rather than carrying a second copy."
        actions={
          <a href={contentUrl} className="text-[13px] text-accent">
            Open the content
          </a>
        }
      />

      <Section>
        <div className="grid gap-4 lg:grid-cols-[1.4fr_1fr]">
          <Panel title="The moment">
            <figure className="space-y-3">
              <img
                src={contentUrl}
                alt={`The frame the product holds for evidence ${id}`}
                className="w-full rounded border border-line bg-bg-elevated"
                onError={(e) => {
                  // The product may hold a moment it cannot serve yet, or one
                  // it has never fetched. The frame says so rather than
                  // showing a broken image.
                  const img = e.currentTarget
                  img.style.display = 'none'
                  const note = img.nextElementSibling as HTMLElement | null
                  if (note) note.style.display = 'block'
                }}
              />
              <p
                style={{ display: 'none' }}
                className="rounded border border-dashed border-line px-4 py-6 text-center text-[13px] leading-[20px] text-text-dim"
              >
                The product has no frame to serve for this evidence yet. That is a fact
                about the fetch, not about the moment.
              </p>
              <figcaption className="text-[12px] leading-[18px] text-text-dim">
                Served by the product at <code className="font-mono">{contentUrl}</code>. The
                digest that fixes these bytes is on the same route.
              </figcaption>
            </figure>
          </Panel>

          <Panel title="What the product holds">
            {isPending ? (
              <p className="text-[13px] leading-[20px] text-text-dim">
                Asking the product about this moment…
              </p>
            ) : isError || !data ? (
              <>
                <p className="text-[13px] leading-[20px] text-text-dim">
                  The product did not answer about this moment. The bytes may still be
                  servable — the content route and the metadata route are different reads —
                  but the metadata is unknown, and an unknown digest is not a digest.
                </p>
                <p className="mt-4 text-[13px] leading-[20px] text-text-dim">
                  A dash is a dash and not a measurement of nothing.
                </p>
              </>
            ) : (
              <>
                <KeyValue
                  items={[
                    { key: 'evidence id', value: <span className="font-mono text-[12px] break-all">{data.evidenceId}</span> },
                    {
                      key: 'kind',
                      value: (
                        <span className="flex items-center gap-2">
                          {data.kind}
                          <Badge tone="neutral">tier {data.tier}</Badge>
                        </span>
                      ),
                    },
                    {
                      key: 'captured at',
                      value: data.capturedAt ?? '—',
                    },
                    {
                      key: 'stored at',
                      value: data.storedAt ?? '—',
                    },
                    {
                      key: 'size',
                      value:
                        data.sizeBytes === undefined
                          ? '—'
                          : `${data.sizeBytes} bytes`,
                    },
                    {
                      key: 'sha-256',
                      value: <span className="font-mono text-[11px] break-all">{data.sha256}</span>,
                    },
                    {
                      key: 'legal hold',
                      value: data.legalHold ? 'yes' : 'no',
                    },
                  ]}
                />
                <p className="mt-4 text-[13px] leading-[20px] text-text-dim">
                  The digest is the product's. The console does not compute it and does not
                  carry a second copy of it: a digest rendered by the console is a digest
                  nobody checked.
                </p>
              </>
            )}
          </Panel>
        </div>
      </Section>

      <Section>
        <EmptyState
          title="The context lives on the incident"
          detail="An evidence row is a moment on its own. The incident that cites it carries the rule, the decision and the reason chain, and that is where the moment becomes evidence."
          action={
            <Link to="/console/incidents" className="text-[13px] text-accent">
              Back to incidents
            </Link>
          }
        />
      </Section>
    </Page>
  )
}
