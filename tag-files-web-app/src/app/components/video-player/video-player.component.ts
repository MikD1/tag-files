import {Component, ElementRef, Input, OnDestroy, OnInit, ViewChild} from '@angular/core';
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
  }

  ngOnDestroy() {
    if (this.player) {
      this.player.dispose();
    }
  }
}
