import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { axe } from '@/test/axe'
import { Checkbox, Field, RadioGroup, Switch } from './Field'

// The form controls.
//
// Every control has a real `<label for>`. The console had ZERO `htmlFor` in
// the whole application, which means every input in it was announced by its
// placeholder and nothing else -- and a placeholder disappears the moment
// somebody types.
//
// The hint is real text. The error is announced.

describe('the field', () => {
  it('associates the label with the control', () => {
    // The control the console used to get wrong, in the whole application.
    // A label that is not a label is a line of text, and a screen reader
    // announces the field as an unlabelled edit.
    render(
      <Field htmlFor="camera-name" label="Camera name">
        <input id="camera-name" />
      </Field>,
    )
    const label = screen.getByText('Camera name')
    expect(label.tagName).toBe('LABEL')
    expect(label).toHaveAttribute('for', 'camera-name')
    expect(screen.getByLabelText('Camera name')).toBeInTheDocument()
  })

  it('shows the hint as real text', () => {
    // A hint that is a placeholder is a hint that disappears when the
    // operator starts typing, which is exactly when they need it.
    render(
      <Field htmlFor="camera-name" label="Camera name" hint="The name in the VMS">
        <input id="camera-name" />
      </Field>,
    )
    const hint = screen.getByText('The name in the VMS')
    expect(hint.tagName).toBe('P')
    expect(hint).toBeInTheDocument()
  })

  it('announces the error and drops the hint', () => {
    // The error is an alert, and it is named after the control it belongs
    // to. A hint and an error at the same time is two answers to one
    // question.
    render(
      <Field
        htmlFor="camera-name"
        label="Camera name"
        hint="The name in the VMS"
        error="The name is required"
      >
        <input id="camera-name" />
      </Field>,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveAttribute('id', 'camera-name-error')
    expect(alert).toHaveTextContent('The name is required')
    expect(screen.queryByText('The name in the VMS')).toBeNull()
  })
})

describe('the checkbox', () => {
  it('associates the label with the box', () => {
    render(<Checkbox id="agree" label="I have read the hand-over" />)
    const box = screen.getByLabelText('I have read the hand-over')
    expect(box).toHaveAttribute('type', 'checkbox')
    expect(box).toHaveAttribute('id', 'agree')
  })

  it('can be ticked by clicking the label', async () => {
    // The label is the control's handle. A checkbox that can only be ticked
    // by hitting a four-pixel box is a checkbox some operators cannot hit.
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Checkbox id="agree" label="I have read the hand-over" onChange={onChange} />)
    await user.click(screen.getByText('I have read the hand-over'))
    expect(onChange).toHaveBeenCalled()
  })
})

describe('the radio group', () => {
  const options = [
    { value: 'comfortable', label: 'comfortable' },
    { value: 'compact', label: 'compact' },
  ]

  it('is a fieldset with a legend, and the options share a name', () => {
    // A group of radios without a name is a group of checkboxes, and the
    // legend is the only thing that says what the choice is.
    render(
      <RadioGroup
        legend="Row density"
        name="density"
        options={options}
        value="comfortable"
        onChange={() => undefined}
      />,
    )
    expect(screen.getByRole('group', { name: 'Row density' })).toBeInTheDocument()
    const radios = screen.getAllByRole('radio')
    expect(radios).toHaveLength(2)
    for (const radio of radios) {
      expect(radio).toHaveAttribute('name', 'density')
    }
  })

  it('reports the option that was chosen', async () => {
    // The value, not the event: the group is a set of VALUES and the caller
    // should not have to know which radio produced it.
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(
      <RadioGroup
        legend="Row density"
        name="density"
        options={options}
        value="comfortable"
        onChange={onChange}
      />,
    )
    await user.click(screen.getByLabelText('compact'))
    expect(onChange).toHaveBeenCalledWith('compact')
  })
})

describe('the switch', () => {
  it('is a switch, and it says which way it is', () => {
    // The state is on the control itself. A switch that is only in the
    // database is a switch nobody can read.
    render(<Switch checked onChange={() => undefined} label="Stream the site" />)
    const sw = screen.getByRole('switch', { name: 'Stream the site' })
    expect(sw).toHaveAttribute('aria-checked', 'true')
  })

  it('says false when it is off', () => {
    render(<Switch checked={false} onChange={() => undefined} label="Stream the site" />)
    expect(screen.getByRole('switch', { name: 'Stream the site' })).toHaveAttribute(
      'aria-checked',
      'false',
    )
  })

  it('cannot submit the form it sits in', () => {
    // A button with no type is a submit button, and a switch inside a form
    // that submits itself is a setting that saves itself.
    render(<Switch checked onChange={() => undefined} label="Stream the site" />)
    expect(screen.getByRole('switch', { name: 'Stream the site' })).toHaveAttribute('type', 'button')
  })

  it('reports the other state when it is clicked', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Switch checked={false} onChange={onChange} label="Stream the site" />)
    await user.click(screen.getByRole('switch', { name: 'Stream the site' }))
    expect(onChange).toHaveBeenCalledWith(true)
  })
})

describe('the controls, scanned', () => {
  it('pass the accessibility scan', async () => {
    // The scan checks the association the way a screen reader reads it, and
    // the association is the thing the console used to get wrong in the
    // whole application.
    const { container } = render(
      <>
        <Field htmlFor="camera-name" label="Camera name" hint="The name in the VMS">
          <input id="camera-name" />
        </Field>
        <Checkbox id="agree" label="I have read the hand-over" />
        <RadioGroup
          legend="Row density"
          name="density"
          options={[
            { value: 'comfortable', label: 'comfortable' },
            { value: 'compact', label: 'compact' },
          ]}
          value="comfortable"
          onChange={() => undefined}
        />
        <Switch checked onChange={() => undefined} label="Stream the site" />
      </>,
    )
    const results = await axe(container)
    expect(results.violations).toEqual([])
  })
})
