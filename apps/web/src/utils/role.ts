/** Badge color for a role — shared so App.vue's own badge and PeoplePage's
 *  roster stay in sync instead of drifting apart one at a time. */
export function roleColor(role: string): 'primary' | 'success' | 'neutral' {
  switch (role) {
    case 'admin':
      return 'primary'
    case 'rider':
      return 'success'
    default:
      return 'neutral'
  }
}
