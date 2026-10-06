import { describe, expect, it } from 'vitest'
import { firstSentences, linerCards, releaseLine, type LinerNotes } from './liner-notes'

const notes: LinerNotes = {
  title: 'Song',
  recordingMbid: 'r',
  year: 1991,
  release: { title: 'Album', type: 'Album', date: '1991-09-24', labels: ['DGC', 'Sub Pop', 'Other'] },
  artist: { mbid: 'a', name: 'Band', bio: 'Band is a band. They play loud. Very loud indeed, for a long time.' },
  credits: [
    { role: 'Written by', names: ['A', 'B'] },
    { role: 'Produced by', names: ['C'] },
  ],
  facts: [{ kind: 'samples', text: 'Samples “X” by Y' }],
}

describe('liner notes', () => {
  it('writes the release line', () => {
    expect(releaseLine(notes)).toBe('Album · 1991 · DGC, Sub Pop')
    expect(releaseLine({ ...notes, year: undefined, release: undefined })).toBe('')
  })

  it('makes cards, facts first', () => {
    const cards = linerCards(notes)
    expect(cards.map((c) => c.kind)).toEqual(['samples', 'release', 'credit', 'credit', 'bio'])
    expect(cards[0].title).toBe('Samples')
    expect(cards[3].text).toBe('C')
    expect(cards[4].title).toBe('About Band')
  })

  it('keeps whole sentences', () => {
    expect(firstSentences('One. Two two. Three three three.', 14)).toBe('One. Two two.')
    expect(firstSentences('A very long first sentence.', 5)).toBe('A very long first sentence.')
    expect(firstSentences('No full stop', 5)).toBe('No full stop')
  })
})
