namespace TagFilesService.Thumbnail;

public interface IThumbnailService
{
    public void EnqueueThumbnailGeneration(uint fileId);
}