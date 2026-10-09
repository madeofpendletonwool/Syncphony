import { useQuery } from '@tanstack/react-query'
import { Clock, Crown, Disc3, Heart, SkipForward } from 'lucide-react'
import { motion } from 'motion/react'
import type { ReactNode } from 'react'
import { AwardsList } from '@/components/games/awards-list'
import { BracketRecap } from '@/components/games/bracket-recap'
import { QueueRow } from '@/components/room/queue-row'
import { RequeueButton } from '@/components/room/requeue-button'
import { Skeleton } from '@/components/ui/skeleton'
import { UserAvatar } from '@/components/user-avatar'
import { formatListening, statsQuery, type Range, type RoomStats } from '@/lib/history'
import { laneStyle } from '@/lib/lane'
import { fadeUp, stagger } from '@/lib/motion'
import type { Night } from '@/lib/nights'
import type { User } from '@/lib/now-playing'
import { usersQuery } from '@/lib/users'

/**
 * What a room played over a range: totals, top tracks and artists, and
 * everyone's share. A recap also shows how the session opened and closed.
 */
export function StatsView({ roomId, range, recap, night }: { roomId: string; range?: Range; recap?: boolean; night?: Night }) {
  const stats = useQuery(statsQuery(roomId, range))
  const users = useQuery(usersQuery)
  const userById = (id: string) => users.data?.find((u) => u.id === id)

  if (stats.isPending) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-24 rounded-3xl" />
        <Skeleton className="h-64 rounded-3xl" />
      </div>
    )
  }
  if (stats.isError) return <p className="px-1 text-sm text-destructive">Couldn&apos;t load the stats.</p>
  const s = stats.data
  if (s.plays === 0) {
    return <p className="glass rounded-3xl p-5 text-sm text-muted-foreground">Nothing has played here yet.</p>
  }

  return (
    <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-6">
      <motion.div variants={fadeUp} className="grid grid-cols-3 gap-2">
        <Tile icon={<Disc3 />} label={s.plays === 1 ? 'song' : 'songs'} value={String(s.plays)} />
        <Tile icon={<Clock />} label="listening" value={formatListening(s.listeningMs)} />
        <Tile icon={<SkipForward />} label="skipped" value={String(s.skipped)} />
      </motion.div>

      {recap && night?.songOfTheNight && (
        <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-1.5 ring-1 ring-amber-400/40">
          <p className="flex items-center gap-1.5 px-3 pt-2 text-caption font-medium tracking-wide text-amber-500 uppercase dark:text-amber-300">
            <Crown className="size-3.5" />
            Song of the night
          </p>
          <QueueRow
            roomId={roomId}
            item={night.songOfTheNight.item}
            user={userById(night.songOfTheNight.item.addedBy)}
            byline
            trailing={
              <>
                <span className="flex shrink-0 items-center gap-1 text-sm text-muted-foreground tabular-nums">
                  <Heart className="size-3.5 fill-rose-500 text-rose-500" />
                  {night.songOfTheNight.hearts}
                </span>
                <RequeueButton item={night.songOfTheNight.item} />
              </>
            }
          />
        </motion.section>
      )}

      {recap && night && night.awards.length > 0 && (
        <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-1.5">
          <Caption>Awards</Caption>
          <AwardsList awards={night.awards} />
        </motion.section>
      )}

      {recap && night?.bracket && (
        <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-1.5">
          <Caption>The bracket</Caption>
          <BracketRecap game={night.bracket} />
        </motion.section>
      )}

      {recap && s.first && s.last && (
        <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-1.5">
          <Caption>Opened with</Caption>
          <QueueRow roomId={roomId} item={s.first.item} user={userById(s.first.item.addedBy)} byline trailing={<RequeueButton item={s.first.item} />} />
          {s.last.item.id !== s.first.item.id && (
            <>
              <Caption>Closed with</Caption>
              <QueueRow roomId={roomId} item={s.last.item} user={userById(s.last.item.addedBy)} byline trailing={<RequeueButton item={s.last.item} />} />
            </>
          )}
        </motion.section>
      )}

      {s.topTracks.length > 0 && (
        <motion.section variants={fadeUp}>
          <Title>Top tracks</Title>
          <ol className="glass flex flex-col rounded-3xl p-1.5">
            {s.topTracks.map((t, i) => (
              <li key={t.item.id}>
                <QueueRow
                  roomId={roomId}
                  item={t.item}
                  user={userById(t.item.addedBy)}
                  byline
                  leading={<span className="w-5 shrink-0 text-center text-sm text-muted-foreground tabular-nums">{i + 1}</span>}
                  trailing={
                    <>
                      <span className="shrink-0 text-caption text-muted-foreground tabular-nums">{plays(t.plays)}</span>
                      <RequeueButton item={t.item} />
                    </>
                  }
                />
              </li>
            ))}
          </ol>
        </motion.section>
      )}

      {s.topArtists.length > 0 && (
        <motion.section variants={fadeUp}>
          <Title>Top artists</Title>
          <ul className="flex flex-wrap gap-2">
            {s.topArtists.map((a, i) => (
              <li key={a.name} className="glass flex items-center gap-2 rounded-full py-1.5 pr-3.5 pl-3 text-sm">
                {i === 0 && <span aria-hidden>👑</span>}
                <span className="font-medium">{a.name}</span>
                <span className="text-muted-foreground tabular-nums">{a.plays}</span>
              </li>
            ))}
          </ul>
        </motion.section>
      )}

      <motion.section variants={fadeUp}>
        <Title>Everyone</Title>
        <ul className="flex flex-col gap-2">
          {s.people.map((p) => (
            <PersonCard key={p.userId} person={p} user={userById(p.userId)} />
          ))}
        </ul>
      </motion.section>
    </motion.div>
  )
}

function PersonCard({ person, user }: { person: RoomStats['people'][number]; user?: User }) {
  const top = person.topTracks[0]
  const artist = person.topArtists[0]
  return (
    <li style={laneStyle(user?.color)} className="glass relative overflow-hidden rounded-3xl p-4">
      <div aria-hidden className="absolute inset-y-0 left-0 w-1 bg-(--lane)" />
      <div className="flex items-center gap-3">
        {user && <UserAvatar user={user} className="size-10 text-sm" />}
        <div className="min-w-0 flex-1">
          <p className="truncate font-medium">{user?.displayName ?? 'Someone'}</p>
          <p className="text-sm text-muted-foreground">
            {plays(person.plays, 'song')} · {formatListening(person.listeningMs)}
            {person.skipped > 0 && ` · ${person.skipped} skipped`}
          </p>
        </div>
      </div>
      {(top || artist) && (
        <dl className="mt-3 grid grid-cols-2 gap-3 text-sm">
          {artist && (
            <div className="min-w-0">
              <dt className="text-caption text-muted-foreground">Top artist</dt>
              <dd className="truncate font-medium">{artist.name}</dd>
            </div>
          )}
          {top && (
            <div className="min-w-0">
              <dt className="text-caption text-muted-foreground">Top track</dt>
              <dd className="truncate font-medium">{top.item.track.title}</dd>
            </div>
          )}
        </dl>
      )}
    </li>
  )
}

function Tile({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return (
    <div className="glass flex flex-col gap-1 rounded-3xl p-4 [&_svg]:size-4 [&_svg]:text-primary">
      {icon}
      <p className="text-title tabular-nums">
        {value}
        <span className="sr-only"> {label}</span>
      </p>
      <p aria-hidden className="text-caption text-muted-foreground">
        {label}
      </p>
    </div>
  )
}

function Title({ children }: { children: ReactNode }) {
  return <h2 className="mb-2 px-1 text-headline">{children}</h2>
}

function Caption({ children }: { children: ReactNode }) {
  return <p className="px-3 pt-2 text-caption font-medium tracking-wide text-muted-foreground uppercase">{children}</p>
}

function plays(n: number, noun = 'play') {
  return `${n} ${noun}${n === 1 ? '' : 's'}`
}
