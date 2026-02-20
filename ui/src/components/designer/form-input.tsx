import { cn } from '@/lib/utils'

interface InputProps {
  label?: string
  value: string
  onChange?: (value: string) => void
  placeholder?: string
  type?: 'text' | 'textarea' | 'select' | 'number'
  hint?: string
  options?: { value: string; label: string }[]
  disabled?: boolean
  className?: string
}

export function FormInput({
  label,
  value,
  onChange,
  placeholder,
  type = 'text',
  hint,
  options,
  disabled,
  className,
}: InputProps) {
  const inputClasses = cn(
    'w-full px-2.5 py-2 rounded-lg border border-border bg-surface-1',
    'text-foreground text-[13px] font-sans',
    'focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary',
    'disabled:opacity-50 disabled:cursor-not-allowed',
    className
  )

  return (
    <div className="mb-3.5">
      {label && (
        <label className="block text-xs font-semibold text-muted-foreground mb-1.5">
          {label}
        </label>
      )}
      {type === 'textarea' ? (
        <textarea
          value={value}
          onChange={(e) => onChange?.(e.target.value)}
          placeholder={placeholder}
          disabled={disabled}
          className={cn(inputClasses, 'resize-y min-h-[60px]')}
        />
      ) : type === 'select' ? (
        <select
          value={value}
          onChange={(e) => onChange?.(e.target.value)}
          disabled={disabled}
          className={inputClasses}
        >
          {placeholder && <option value="">{placeholder}</option>}
          {options?.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      ) : (
        <input
          type={type}
          value={value}
          onChange={(e) => onChange?.(e.target.value)}
          placeholder={placeholder}
          disabled={disabled}
          className={inputClasses}
        />
      )}
      {hint && (
        <div className="text-[11px] text-muted-foreground mt-1">{hint}</div>
      )}
    </div>
  )
}

interface SliderInputProps {
  label: string
  value: number
  onChange?: (value: number) => void
  min: number
  max: number
  step?: number
  hint?: string
}

export function SliderInput({
  label,
  value,
  onChange,
  min,
  max,
  step = 0.1,
  hint,
}: SliderInputProps) {
  return (
    <div className="mb-3.5">
      <div className="flex justify-between items-center mb-1.5">
        <label className="text-xs font-semibold text-muted-foreground">
          {label}
        </label>
        <span className="text-xs font-mono text-foreground">{value}</span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange?.(parseFloat(e.target.value))}
        className="w-full h-1.5 bg-surface-2 rounded-lg appearance-none cursor-pointer accent-primary"
      />
      {hint && (
        <div className="text-[11px] text-muted-foreground mt-1">{hint}</div>
      )}
    </div>
  )
}
