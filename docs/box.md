# The Syncphony box

A Syncphony box is a Raspberry Pi 4 or 5 plugged into a TV that becomes a
room's **big screen and its speaker**, with nothing to click: it boots
straight into the room, plays by itself, survives power cuts, and updates
itself overnight. It's a kiosk for your Syncphony server and doesn't run a
server of its own.

The box has its own repository:
**[madeofpendletonwool/syncphony-box](https://github.com/madeofpendletonwool/syncphony-box)**.
Download an image from its
**[releases](https://github.com/madeofpendletonwool/syncphony-box/releases)**,
or use Raspberry Pi Imager with the box's listing (below).

## Make one

1. In [Raspberry Pi Imager](https://www.raspberrypi.com/software/), open
   **App Options → Content Repository → Use custom URL** and paste
   `https://madeofpendletonwool.github.io/syncphony-box/imager/os_list.json`.
2. Choose your Pi, then **Syncphony Box**, then the SD card. When Imager
   offers to customize, set a username and password and your Wi-Fi (skip
   it if the box will be on a cable). Write the card.
3. Put the card in the Pi, plug it into the TV, and power it on.
4. The TV shows a QR code. Scan it with a phone on the same network and
   type this server's address. (No Wi-Fi set? The box opens its own
   `Syncphony-Box-XXXX` network first, so you can give it yours.)

## Pair it with a room

The TV now shows a pairing code. In the room, open the **big screen**
dialog and type the code in. Because it's a box, the dialog already
suggests its name and turns **Play the audio on it too** on. Pair, and
the box plays the room by itself from then on, through reboots and power
cuts.

## Look after it from your phone

The box shows up in the room's **Paired screens** like any screen, with
more to say:

- **"Syncphony Box 1.3 · online · 52 °C"**: its software version, whether
  it's connected, and its temperature.
- **Warnings**: *running hot / throttled* (it needs airflow, or a better
  power supply), *update failed* (it keeps running what it had and tries
  again the next night), and *box software older than this server
  expects* (it should update overnight; if it doesn't, reflash it).
- **Reload** restarts the box's browser. **Restart box** reboots it, and
  it asks for a second tap. Both are for the room's owner, whoever paired
  the screen, or an admin. A screen that isn't a box only has Reload.

The box updates itself every night around 04:00 (never while music
plays). More on hardware, the config file and troubleshooting is in the
[box repository's README](https://github.com/madeofpendletonwool/syncphony-box#readme).

How the page and the box talk is
[ADR 0016](adr/0016-syncphony-box.md).
