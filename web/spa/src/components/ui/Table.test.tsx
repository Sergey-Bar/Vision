import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { KeyValue, Table, type Column } from './Table'

// The table.
//
// One table style, and the numbers are aligned: a dense console is read by
// scanning a column, and a number that is not in the same place in every row
// is a number that cannot be compared by eye.
//
// Every cell is a cell. The header is a header, and it says what the column
// holds.

interface Row {
  id: string
  camera: string
  frames: number
}

const rows: Row[] = [
  { id: 'r-1', camera: 'Loading bay', frames: 42 },
  { id: 'r-2', camera: 'Gate', frames: 7 },
]

const columns: Column<Row>[] = [
  { key: 'camera', header: 'Camera', render: (r) => r.camera },
  { key: 'frames', header: 'Frames', numeric: true, render: (r) => r.frames },
]

describe('the table', () => {
  it('says what each column holds', () => {
    // A header cell that is not a header cell is a label, and a screen
    // reader cannot say which column a number belongs to.
    render(<Table columns={columns} rows={rows} rowKey={(r) => r.id} />)
    const camera = screen.getByRole('columnheader', { name: 'Camera' })
    expect(camera).toHaveAttribute('scope', 'col')
    expect(screen.getByRole('columnheader', { name: 'Frames' })).toHaveAttribute('scope', 'col')
  })

  it('right-aligns the numbers, in the header and in the cell', () => {
    // The column is scanned, so the numbers have to be in the same place in
    // every row. A number column that is left-aligned is a column of
    // different widths.
    render(<Table columns={columns} rows={rows} rowKey={(r) => r.id} />)
    expect(screen.getByRole('columnheader', { name: 'Frames' })).toHaveClass('text-right')
    expect(screen.getByRole('cell', { name: '42' })).toHaveClass('text-right')
    expect(screen.getByRole('cell', { name: 'Loading bay' })).not.toHaveClass('text-right')
  })

  it('renders the empty state instead of an empty table', () => {
    // A table with no rows and no empty state is a header with nothing under
    // it, and the reader is left guessing whether there is nothing there or
    // whether the question was never asked.
    const { container } = render(
      <Table
        columns={columns}
        rows={[]}
        rowKey={(r) => r.id}
        empty={<p>No camera has reported yet</p>}
      />,
    )
    expect(container.querySelector('table')).toBeNull()
    expect(screen.getByText('No camera has reported yet')).toBeInTheDocument()
  })

  it('keeps a row action on the keyboard', async () => {
    // The console is a keyboard instrument. A row whose only action is a
    // button that the keyboard cannot reach is a row with no action.
    const user = userEvent.setup()
    const onOpen = vi.fn()
    const withAction: Column<Row>[] = [
      ...columns,
      {
        key: 'action',
        header: 'Action',
        render: (r) => (
          <button
            type="button"
            onClick={() => {
              onOpen(r.id)
            }}
          >
            Open
          </button>
        ),
      },
    ]
    render(<Table columns={withAction} rows={rows} rowKey={(r) => r.id} />)

    const buttons = screen.getAllByRole('button', { name: 'Open' })
    expect(buttons).toHaveLength(2)
    const button = buttons[0]
    expect(button).toBeInTheDocument()
    expect(button.tabIndex).toBe(0)

    await user.tab()
    expect(button).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(onOpen).toHaveBeenCalledWith('r-1')
  })
})

describe('the key/value list', () => {
  it('is a description list, with a term and a value per fact', () => {
    // The shape most of a console's facts take. A <div> soup of labels is
    // not a list a screen reader can walk.
    const { container } = render(
      <KeyValue
        items={[
          { key: 'tenant', value: 'tenant-demo' },
          { key: 'site', value: 'site-demo' },
        ]}
      />,
    )
    const list = container.querySelector('dl')
    expect(list).not.toBeNull()
    expect(container.querySelectorAll('dt')).toHaveLength(2)
    expect(container.querySelectorAll('dd')).toHaveLength(2)
    expect(screen.getByText('tenant')).toBeInTheDocument()
    expect(screen.getByText('site-demo')).toBeInTheDocument()
  })
})
