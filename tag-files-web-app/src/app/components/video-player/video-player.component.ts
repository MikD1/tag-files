import {Component, ElementRef, HostListener, Input, OnDestroy, OnInit, ViewChild} from '@angular/core';
import videojs from 'video.js';
import VideoJsPlayer from "video.js/dist/types/player"

@Component({
  selector: 'app-video-player',
  imports: [],
  templateUrl: './video-player.component.html',
  styleUrl: './video-player.component.scss'
})
export class VideoPlayerComponent implements OnInit, OnDestroy {
  @ViewChild('target', {static: true}) target!: ElementRef;
  @Input() options: any;

  private player: VideoJsPlayer | undefined;

  ngOnInit() {
    this.player = videojs(this.target.nativeElement, this.options);
    this.player.volume(0.5);
  }

  ngOnDestroy() {
    if (this.player) {
      this.player.dispose();
    }
  }

  @HostListener('window:keydown', ['$event'])
  handleKeyDown(event: KeyboardEvent) {
    if (!this.player) {
      return;
    }

    event.preventDefault(); // Prevent page scroll
    switch (event.key) {
      case ' ':
        this.player.paused() ? this.player.play() : this.player.pause();
        break;
      case 'ArrowRight':
        this.player.currentTime(this.player.currentTime()! + 5);
        break;
      case 'ArrowLeft':
        this.player.currentTime(Math.max(0, this.player.currentTime()! - 3));
        break;
      case 'ArrowUp':
        this.player.volume(Math.min(1, this.player.volume()! + 0.1));
        break;
      case 'ArrowDown':
        this.player.volume(Math.max(0, this.player.volume()! - 0.1));
        break;
    }
  }
}
