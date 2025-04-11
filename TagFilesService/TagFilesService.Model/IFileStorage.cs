namespace TagFilesService.Model;

public interface IFileStorage
{
    Task UploadFile(string bucketName, string fileName, Stream fileStream, long fileSize, string contentType);
}