using System.Linq.Dynamic.Core;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class MetadataService(AppDbContext dbContext) : IMetadataService
{
    public async Task<FileMetadata> SaveMetadata(FileMetadata metadata)
    {
        if (metadata.Id == 0)
        {
            dbContext.FilesMetadata.Add(metadata);
        }
        else
        {
            dbContext.FilesMetadata.Update(metadata);
        }

        await dbContext.SaveChangesAsync();
        return metadata;
    }

    public async Task<FileMetadata> GetMetadata(uint id)
    {
        return await GetMetadataByIdOrThrow(id);
    }

    public async Task<List<FileMetadata>> GetLastMetadataItems(int count)
    {
        return await dbContext.FilesMetadata
            .Include(x => x.Tags)
            .OrderByDescending(x => x.UploadedOn)
            .Take(count)
            .ToListAsync();
    }

    public async Task<FileMetadata> AssignTags(uint metadataId, List<uint> tagIds)
    {
        FileMetadata metadata = await GetMetadataByIdOrThrow(metadataId);
        List<Tag> tags = await dbContext.Tags
            .Where(x => tagIds.Contains(x.Id))
            .ToListAsync();

        if (tags.Count != tagIds.Count)
        {
            throw new ApplicationException("Some tags not found");
        }

        metadata.Tags.Clear();
        metadata.Tags.AddRange(tags);

        await dbContext.SaveChangesAsync();
        return metadata;
    }

    public async Task<IPaginatedList<FileMetadata>> Search(string tagQuery, int pageIndex, int pageSize)
    {
        IQueryable<FileMetadata> queryable = dbContext.FilesMetadata
            .Include(x => x.Tags);

        if (!string.IsNullOrWhiteSpace(tagQuery))
        {
            string dynamicQuery = TagQueryConverter.ConvertToDynamicQuery(tagQuery);
            queryable = queryable
                .Where(dynamicQuery);
        }

        queryable = queryable
            .OrderByDescending(x => x.UploadedOn);
        return await PaginatedList<FileMetadata>.CreateAsync(queryable, pageIndex, pageSize);
    }

    private async Task<FileMetadata> GetMetadataByIdOrThrow(uint id)
    {
        FileMetadata? metadata = await dbContext.FilesMetadata
            .Include(x => x.Tags)
            .FirstOrDefaultAsync(x => x.Id == id);
        if (metadata is null)
        {
            throw new ApplicationException($"Metadata {id} not found");
        }

        return metadata;
    }
}